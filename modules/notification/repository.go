package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the notification rows the procedures manage.
// Every method takes the query surface, so the service passes either the
// pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// notificationColumns are the columns the notification procedures read, in
// scan order, spelled for the queries that name one table.
var notificationColumns = []string{
	"id", "category", "topic", "title", "body", "audience_kind",
	"created_by", "cancelled_at", "email_sent_at", "created_at", "updated_at",
}

// qualifiedColumns is the same list spelled for the inbox query, where the
// account table is not joined but the receipt subquery is — the alias keeps
// the column names unambiguous all the same.
func qualifiedColumns() []string {
	qualified := make([]string, 0, len(notificationColumns))
	for _, column := range notificationColumns {
		qualified = append(qualified, "n."+column)
	}
	return qualified
}

// listSortColumns is the whitelist an administrative list's sort key
// resolves through. The names are the wire values the request validates
// against.
var listSortColumns = map[string]string{
	"category":   "category",
	"topic":      "lower(topic)",
	"title":      "lower(title)",
	"created_at": "created_at",
}

// scanNotification reads one row into the schema.
func scanNotification(scan func(dest ...any) error) (Notification, error) {
	var row Notification
	err := scan(
		&row.ID, &row.Category, &row.Topic, &row.Title, &row.Body, &row.AudienceKind,
		&row.CreatedBy, &row.CancelledAt, &row.EmailSentAt, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return Notification{}, err
	}
	return row, nil
}

// Create stores the notification row and its audience junctions, and
// answers its identifier. The audience rows are written in the same
// transaction the notification is — a notification whose audience never
// landed is a message with no readers, not a partial state.
func (r *Repository) Create(ctx context.Context, db datastore.Querier, row Notification, userIDs, groupIDs []uuid.UUID) error {
	if row.ID == uuid.Nil() {
		row.ID = uuid.NewV7()
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableNotifications)
	ib.Cols("id", "category", "topic", "title", "body", "audience_kind", "created_by")
	ib.Values(row.ID, row.Category, row.Topic, row.Title, row.Body, row.AudienceKind, row.CreatedBy)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("notification: create: %w", err)
	}

	if err := r.insertAudience(ctx, db, entity.TableNotificationUsers, "user_id", row.ID, userIDs); err != nil {
		return err
	}
	return r.insertAudience(ctx, db, entity.TableNotificationUserGroups, "user_group_id", row.ID, groupIDs)
}

// insertAudience writes one junction's rows. An empty list writes nothing —
// a global notification names no account and no group.
func (r *Repository) insertAudience(ctx context.Context, db datastore.Querier, table, column string, id uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(table)
	ib.Cols("notification_id", column)
	for _, target := range ids {
		ib.Values(id, target)
	}

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("notification: create audience: %w", err)
	}
	return nil
}

// Get reads one notification by its identifier, with the audiences its
// kind names. An identifier that names no notification is the caller's
// not-found failure.
func (r *Repository) Get(ctx context.Context, db datastore.Querier, id uuid.UUID) (Notification, []uuid.UUID, []uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(notificationColumns...)
	sb.From(entity.TableNotifications)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanNotification(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return Notification{}, nil, nil, datastore.ErrNoRows
	}
	if err != nil {
		return Notification{}, nil, nil, fmt.Errorf("notification: get: %w", err)
	}

	userIDs, err := r.audienceIDs(ctx, db, entity.TableNotificationUsers, "user_id", id)
	if err != nil {
		return Notification{}, nil, nil, err
	}
	groupIDs, err := r.audienceIDs(ctx, db, entity.TableNotificationUserGroups, "user_group_id", id)
	if err != nil {
		return Notification{}, nil, nil, err
	}
	return row, userIDs, groupIDs, nil
}

// audienceIDs reads one junction's target identifiers, ordered so the wire
// view is stable.
func (r *Repository) audienceIDs(ctx context.Context, db datastore.Querier, table, column string, id uuid.UUID) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(column)
	sb.From(table)
	sb.Where(sb.Equal("notification_id", id))
	sb.OrderBy(column)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notification: audience: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var target uuid.UUID
		if err := rows.Scan(&target); err != nil {
			return nil, fmt.Errorf("notification: audience: %w", err)
		}
		ids = append(ids, target)
	}
	return ids, rows.Err()
}

// ListAll answers one page of every notification, ordered as the caller
// asked (absent a choice, newest first). A category name narrows the page;
// an empty one lists both. A cancelled notification stays listed: the
// withdrawal is a stamp the view carries, not a deletion.
func (r *Repository) ListAll(ctx context.Context, db datastore.Querier, category, sortBy string, ascending bool, offset, limit int) ([]Notification, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(notificationColumns...)
	sb.From(entity.TableNotifications)
	if category != "" {
		sb.Where(sb.Equal("category", category))
	}
	sb.OrderBy(datastore.ListOrder(listSortColumns, sortBy, "created_at", ascending), "id")
	sb.Limit(limit).Offset(offset)

	rows, err := r.notificationRows(ctx, db, sb, "notification: list")
	if err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableNotifications)
	if category != "" {
		cb.Where(cb.Equal("category", category))
	}
	query, args := cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("notification: count: %w", err)
	}
	return rows, total, nil
}

// Cancel stamps the withdrawal instant on one live notification. The WHERE
// clause excludes an already-cancelled row, so a second withdrawal answers
// false — the state it names is the one the notification is already in.
func (r *Repository) Cancel(ctx context.Context, db datastore.Querier, id uuid.UUID, at time.Time) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableNotifications)
	ub.Set(ub.Assign("cancelled_at", at))
	ub.Where(ub.Equal("id", id), ub.IsNull("cancelled_at"))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("notification: cancel: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkEmailSent records that the email pass ran for one notification, so a
// retry never repeats it.
func (r *Repository) MarkEmailSent(ctx context.Context, db datastore.Querier, id uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableNotifications)
	ub.Set(ub.Assign("email_sent_at", at))
	ub.Where(ub.Equal("id", id), ub.IsNull("email_sent_at"))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("notification: mark email sent: %w", err)
	}
	return nil
}

// visibleWhere is the visibility predicate every account-side query shares:
// the notification must be live, and the account must be one it targets —
// by the whole deployment, by name, or through a group's membership. The
// `n` alias is the notification the caller's FROM names.
func visibleWhere(sb *sqlbuilder.SelectBuilder, userID uuid.UUID) {
	userSub := sqlbuilder.PostgreSQL.NewSelectBuilder()
	userSub.Select("1")
	userSub.From(entity.TableNotificationUsers + " nu")
	userSub.Where(userSub.Equal("nu.user_id", userID), "nu.notification_id = n.id")

	groupSub := sqlbuilder.PostgreSQL.NewSelectBuilder()
	groupSub.Select("1")
	groupSub.From(entity.TableNotificationUserGroups + " ng")
	groupSub.Join(entity.TableUserGroupsUsers+" ug", "ug.user_group_id = ng.user_group_id")
	groupSub.Where(groupSub.Equal("ug.user_id", userID), "ng.notification_id = n.id")

	sb.Where(
		sb.IsNull("n.cancelled_at"),
		sb.Or(
			sb.Equal("n.audience_kind", AudienceGlobal),
			sb.Exists(userSub),
			sb.Exists(groupSub),
		),
	)
}

// readAtExpression is the scalar subquery that reads one account's receipt
// beside the notification. The account's identifier is bound through the
// outer builder's variable, so the placeholder order follows the statement's
// compile order — the select clause before the where clause.
func readAtExpression(sb *sqlbuilder.SelectBuilder, userID uuid.UUID) string {
	return "(SELECT r.read_at FROM " + entity.TableNotificationReads + " r" +
		" WHERE r.notification_id = n.id AND r.user_id = " + sb.Var(userID) + ")"
}

// unreadCondition is the predicate that excludes a notification the account
// already holds a receipt for. It is spelled against the receipt table
// rather than the selected alias, because a WHERE clause cannot name a
// select alias.
func unreadCondition(sb *sqlbuilder.SelectBuilder, userID uuid.UUID) string {
	sub := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sub.Select("1")
	sub.From(entity.TableNotificationReads + " r")
	sub.Where(sub.Equal("r.user_id", userID), "r.notification_id = n.id")
	return "NOT " + sb.Exists(sub)
}

// inboxSortColumns is the whitelist the inbox's sort key resolves through,
// spelled against the `n` alias the inbox query names. The names are the
// wire values the request validates against.
var inboxSortColumns = map[string]string{
	"category":   "n.category",
	"topic":      "lower(n.topic)",
	"title":      "lower(n.title)",
	"created_at": "n.created_at",
}

// ListInbox answers one page of the live notifications the account is
// targeted by, newest first, each with the instant the account read it.
// The category name narrows the page; the sort key orders it.
func (r *Repository) ListInbox(ctx context.Context, db datastore.Querier, userID uuid.UUID, unreadOnly bool, category, sortBy string, ascending bool, offset, limit int) ([]InboxRow, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(qualifiedColumns()...)
	sb.SelectMore(readAtExpression(sb, userID) + " AS read_at")
	sb.From(entity.TableNotifications + " n")
	visibleWhere(sb, userID)
	if unreadOnly {
		sb.Where(unreadCondition(sb, userID))
	}
	if category != "" {
		sb.Where(sb.Equal("n.category", category))
	}
	sb.OrderBy(datastore.ListOrder(inboxSortColumns, sortBy, "created_at", ascending), "n.id")
	sb.Limit(limit).Offset(offset)

	inbox, err := r.inboxRows(ctx, db, sb, "notification: inbox")
	if err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableNotifications + " n")
	visibleWhere(cb, userID)
	if unreadOnly {
		cb.Where(unreadCondition(cb, userID))
	}
	if category != "" {
		cb.Where(cb.Equal("n.category", category))
	}
	query, args := cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("notification: inbox count: %w", err)
	}
	return inbox, total, nil
}

// UnreadCount answers how many live notifications target the account and
// carry no receipt yet.
func (r *Repository) UnreadCount(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableNotifications + " n")
	visibleWhere(cb, userID)
	cb.Where(unreadCondition(cb, userID))

	query, args := cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("notification: unread count: %w", err)
	}
	return total, nil
}

// IsVisible answers whether one live notification targets the account. It
// is the mark-read gate: a receipt is written for a notification the
// account can see, never for one it cannot name.
func (r *Repository) IsVisible(ctx context.Context, db datastore.Querier, id, userID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(entity.TableNotifications + " n")
	sb.Where(sb.Equal("n.id", id))
	visibleWhere(sb, userID)

	query, args := sb.Build()
	err := db.QueryRow(ctx, query, args...).Scan(new(any))
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("notification: visibility: %w", err)
	}
	return true, nil
}

// InsertRead writes one receipt. The ON CONFLICT keeps the first read_at:
// marking an already-read notification again is the same success, and the
// instant it was read does not move.
func (r *Repository) InsertRead(ctx context.Context, db datastore.Querier, id, userID uuid.UUID) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableNotificationReads)
	ib.Cols("notification_id", "user_id")
	ib.Values(id, userID)

	query, args := ib.Build()
	query += " ON CONFLICT DO NOTHING"
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("notification: insert read: %w", err)
	}
	return nil
}

// MarkAllRead writes the missing receipts for every live notification the
// account is targeted by, and answers how many it marked. The statement is
// the select the visibility predicate is built into, wrapped in the insert
// that consumes it — one statement, so the receipts and the visibility they
// name are decided together.
func (r *Repository) MarkAllRead(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("n.id", sb.Var(userID))
	sb.From(entity.TableNotifications + " n")
	visibleWhere(sb, userID)
	sb.Where(unreadCondition(sb, userID))

	selectQuery, selectArgs := sb.Build()
	query := "INSERT INTO " + entity.TableNotificationReads + " (notification_id, user_id) " +
		selectQuery + " ON CONFLICT DO NOTHING"

	tag, err := db.Exec(ctx, query, selectArgs...)
	if err != nil {
		return 0, fmt.Errorf("notification: mark all read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Recipients answers one keyset page of the accounts a live notification
// targets, with the address its email pass goes to. The page runs through
// the same visibility predicate the inbox reads — global resolves to every
// enabled account, the named kinds to their junctions — so the audience a
// reader sees and the audience an email goes to cannot drift apart. The
// cancelled check rides the join, so a notification withdrawn mid-pass
// stops delivering.
func (r *Repository) Recipients(ctx context.Context, db datastore.Querier, id, after uuid.UUID, limit int) ([]Recipient, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id", "u.email", "u.display_name")
	sb.From(entity.TableUsers + " u")
	sb.Join(entity.TableNotifications+" n", "n.id = "+sb.Var(id))
	sb.Where(sb.Equal("u.disabled", false), sb.GT("u.id", after))
	visibleWhereFor(sb, id)
	sb.OrderBy("u.id")
	sb.Limit(limit)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notification: recipients: %w", err)
	}
	defer rows.Close()

	recipients := []Recipient{}
	for rows.Next() {
		var row Recipient
		if err := rows.Scan(&row.ID, &row.Email, &row.DisplayName); err != nil {
			return nil, fmt.Errorf("notification: recipients: %w", err)
		}
		recipients = append(recipients, row)
	}
	return recipients, rows.Err()
}

// visibleWhereFor is the visibility predicate for the recipient query,
// where the account is the FROM's own table and the notification is bound
// by parameter rather than by alias.
func visibleWhereFor(sb *sqlbuilder.SelectBuilder, id uuid.UUID) {
	userSub := sqlbuilder.PostgreSQL.NewSelectBuilder()
	userSub.Select("1")
	userSub.From(entity.TableNotificationUsers + " nu")
	userSub.Where(userSub.Equal("nu.notification_id", id), "nu.user_id = u.id")

	groupSub := sqlbuilder.PostgreSQL.NewSelectBuilder()
	groupSub.Select("1")
	groupSub.From(entity.TableNotificationUserGroups + " ng")
	groupSub.Join(entity.TableUserGroupsUsers+" ug", "ug.user_group_id = ng.user_group_id")
	groupSub.Where(groupSub.Equal("ng.notification_id", id), "ug.user_id = u.id")

	sb.Where(
		sb.IsNull("n.cancelled_at"),
		sb.Or(
			sb.Equal("n.audience_kind", AudienceGlobal),
			sb.Exists(userSub),
			sb.Exists(groupSub),
		),
	)
}

// GroupMemberUserIDs answers the accounts that belong to any of the named
// groups, deduplicated. It is what the live tail resolves a `user_groups`
// audience into: the members at publish time, not the members at read
// time — a stream event is delivered to who is connected, the list is what
// settles who the audience was.
func (r *Repository) GroupMemberUserIDs(ctx context.Context, db datastore.Querier, groupIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(groupIDs) == 0 {
		return []uuid.UUID{}, nil
	}

	members := make([]any, 0, len(groupIDs))
	for _, id := range groupIDs {
		members = append(members, id)
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("DISTINCT ug.user_id")
	sb.From(entity.TableUserGroupsUsers + " ug")
	sb.Where(sb.In("ug.user_group_id", members...))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notification: group members: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("notification: group members: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// notificationRows runs a built page query over the notification table and
// scans the rows it answers.
func (r *Repository) notificationRows(ctx context.Context, db datastore.Querier, sb *sqlbuilder.SelectBuilder, cause string) ([]Notification, error) {
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	defer rows.Close()

	notifications := []Notification{}
	for rows.Next() {
		row, err := scanNotification(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cause, err)
		}
		notifications = append(notifications, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	return notifications, nil
}

// inboxRows runs a built inbox query and scans the rows it answers.
func (r *Repository) inboxRows(ctx context.Context, db datastore.Querier, sb *sqlbuilder.SelectBuilder, cause string) ([]InboxRow, error) {
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	defer rows.Close()

	inbox := []InboxRow{}
	for rows.Next() {
		row, err := scanInbox(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cause, err)
		}
		inbox = append(inbox, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", cause, err)
	}
	return inbox, nil
}

// scanInbox reads one inbox row: the notification columns, then the
// receipt.
func scanInbox(scan func(dest ...any) error) (InboxRow, error) {
	var row InboxRow
	if err := scan(append(notificationScanDests(&row.Notification), &row.ReadAt)...); err != nil {
		return InboxRow{}, err
	}
	return row, nil
}

// notificationScanDests is the scan destinations of one notification row,
// in column order.
func notificationScanDests(row *Notification) []any {
	return []any{
		&row.ID, &row.Category, &row.Topic, &row.Title, &row.Body, &row.AudienceKind,
		&row.CreatedBy, &row.CancelledAt, &row.EmailSentAt, &row.CreatedAt, &row.UpdatedAt,
	}
}
