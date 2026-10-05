package seeders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/modules/identity/usergroup"
	"github.com/riipandi/saka/modules/notification"
	"github.com/riipandi/saka/pkg/strutils"
)

// NotificationSeederName is the name this seeder reports under.
const NotificationSeederName = "NotificationSeeder"

// scenarioNotifications are the notices a development database carries: one
// announcement every account sees, and one system notice aimed at a single
// group. Together they cover both categories, both audience kinds, and the
// junction a targeted publish writes.
//
// A notification has no natural key — the table carries no unique index — so
// idempotency here is the seeder's own title lookup rather than a database
// conflict.
var scenarioNotifications = []scenarioNotification{
	{
		title:  "Scheduled maintenance this weekend",
		topic:  "Maintenance",
		body:   "The service will be briefly unavailable on Sunday morning.",
		typ:    "announcement",
		kind:   "global",
		readBy: []string{DefaultUser.Username},
	},
	{
		title:  "Welcome to the development database",
		body:   "This notice was seeded for the editors group; one member has read it.",
		typ:    "system",
		kind:   "user_groups",
		group:  "editors",
		readBy: []string{"hermione_granger"},
	},
}

// scenarioNotification is one notice the seeder writes: the copy, the
// category and audience kind the checks police, the group a targeted
// audience names, and the usernames whose read receipts it carries.
type scenarioNotification struct {
	title  string
	topic  string
	body   string
	typ    string
	kind   string
	group  string
	readBy []string
}

// Notification returns the seeder for the development notices. It runs last:
// a targeted audience names a group the group seeder wrote, and the receipts
// name accounts the user seeder wrote.
func Notification() Seeder {
	return Seeder{
		Name:  NotificationSeederName,
		Apply: applyNotifications,
	}
}

// applyNotifications publishes the scenario notices and their read
// receipts.
//
// The title lookup is the idempotency: a notice this run finds reports as
// skipped and its receipts are left to their own primary key. A receipt for
// an account the database does not hold is skipped, the way every seeder
// treats a row it cannot anchor.
func applyNotifications(
	ctx context.Context,
	q datastore.Querier,
	dryRun bool,
) (created, skipped []string, err error) {
	authorID, err := userIDByEmail(ctx, q, DefaultUser.Email)
	if err != nil {
		return nil, nil, err
	}
	// The notices are the default account's publications, but a standalone
	// run of this seeder carries no account to name — the column is
	// nullable, so the notice lands unauthored instead of failing.
	var author any
	if authorID != (uuid.UUID{}) {
		author = authorID
	}

	for i := range scenarioNotifications {
		notice := scenarioNotifications[i]

		noticeID, inserted, noticeErr := insertNotification(ctx, q, notice, author, dryRun)
		if noticeErr != nil {
			return nil, nil, noticeErr
		}
		if inserted {
			created = append(created, notice.title)
		} else {
			skipped = append(skipped, notice.title)
		}

		if notice.group != "" && !noticeID.IsZero() {
			groupID, groupErr := groupIDByName(ctx, q, notice.group)
			if groupErr != nil {
				return nil, nil, groupErr
			}
			written, audErr := insertGroupAudience(ctx, q, noticeID, groupID, dryRun)
			if audErr != nil {
				return nil, nil, audErr
			}
			line := notice.group + " audience of " + notice.title
			if written {
				created = append(created, line)
			} else {
				skipped = append(skipped, line)
			}
		}

		for _, username := range notice.readBy {
			if noticeID.IsZero() {
				break
			}
			written, readErr := insertReadReceipt(ctx, q, noticeID, username, dryRun)
			if readErr != nil {
				return nil, nil, readErr
			}
			line := username + " read " + notice.title
			if written {
				created = append(created, line)
			} else {
				skipped = append(skipped, line)
			}
		}
	}
	return created, skipped, nil
}

// insertNotification writes one notice row and answers its identifier plus
// whether this run created it. A dry run answers a zero identifier — the
// receipts have no row to attach to — and plans by the title lookup alone.
func insertNotification(ctx context.Context, q datastore.Querier, notice scenarioNotification, author any, dryRun bool) (notification.NotificationID, bool, error) {
	exists, err := notificationExists(ctx, q, notice.title)
	if err != nil {
		return notification.NotificationID{}, false, err
	}
	if exists {
		id, idErr := notificationIDByTitle(ctx, q, notice.title)
		if idErr != nil {
			return notification.NotificationID{}, false, idErr
		}
		return id, false, nil
	}
	if dryRun {
		return notification.NotificationID{}, true, nil
	}

	// A system notice names its audience explicitly, so it never carries a
	// topic; an announcement may. The check constraint on the table
	// enforces the first half, the scenario definitions the second.
	topic := notice.topic
	if notice.typ == "system" {
		topic = ""
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableNotifications)
	ib.Cols("id", "category", "topic", "title", "body", "audience_kind", "created_by", "created_at")
	ib.Values(uuid.NewV7(), notice.typ, nullable(topic), notice.title, notice.body, notice.kind, author, time.Now().UTC())
	ib.Returning("id")

	query, args := ib.Build()
	var rawID string
	if scanErr := q.QueryRow(ctx, query, args...).Scan(&rawID); scanErr != nil {
		return notification.NotificationID{}, false, fmt.Errorf("notification seeder: %s: %w", notice.title, scanErr)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return notification.NotificationID{}, false, fmt.Errorf("notification seeder: %s: %w", notice.title, err)
	}
	id, err := notification.IDFromUUID(parsed)
	if err != nil {
		return notification.NotificationID{}, false, fmt.Errorf("notification seeder: %s: %w", notice.title, err)
	}
	return id, true, nil
}

// insertGroupAudience writes the junction row one targeted notice needs. It
// only runs for a notice the database already holds or this run created — a
// dry run carries no identifier to attach.
func insertGroupAudience(ctx context.Context, q datastore.Querier, noticeID notification.NotificationID, groupID usergroup.GroupID, dryRun bool) (bool, error) {
	if dryRun {
		return true, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableNotificationUserGroups)
	ib.Cols("notification_id", "user_group_id")
	ib.Values(strutils.ToUUID(noticeID), usergroup.IDToUUID(groupID))
	ib.SQL("ON CONFLICT DO NOTHING")

	query, args := ib.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("notification seeder: audience: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// insertReadReceipt stamps one account's read state. An account the database
// does not hold is skipped, on a real run and in a plan alike.
func insertReadReceipt(ctx context.Context, q datastore.Querier, noticeID notification.NotificationID, username string, dryRun bool) (bool, error) {
	userID, err := userIDByName(ctx, q, username)
	if err != nil {
		return false, err
	}
	if userID == (uuid.UUID{}) {
		return false, nil
	}

	if dryRun {
		exists, existsErr := readReceiptExists(ctx, q, noticeID, userID)
		if existsErr != nil {
			return false, existsErr
		}
		return !exists, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableNotificationReads)
	ib.Cols("notification_id", "user_id", "read_at")
	ib.Values(strutils.ToUUID(noticeID), userID, time.Now().UTC())
	ib.SQL("ON CONFLICT DO NOTHING")

	query, args := ib.Build()
	tag, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("notification seeder: receipt %s: %w", username, err)
	}
	return tag.RowsAffected() > 0, nil
}

// notificationExists reports whether a notice with the title already sits in
// the table — the lookup that stands in for the unique index the table does
// not carry.
func notificationExists(ctx context.Context, q datastore.Querier, title string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(entity.TableNotifications).Where(sb.Equal("title", title))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// notificationIDByTitle reads the identifier of the notice the title names.
func notificationIDByTitle(ctx context.Context, q datastore.Querier, title string) (notification.NotificationID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(entity.TableNotifications).Where(sb.Equal("title", title))

	query, args := sb.Build()
	var rawID string
	if err := q.QueryRow(ctx, query, args...).Scan(&rawID); err != nil {
		return notification.NotificationID{}, fmt.Errorf("notification seeder: %s: %w", title, err)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		return notification.NotificationID{}, fmt.Errorf("notification seeder: %s: %w", title, err)
	}
	id, err := notification.IDFromUUID(parsed)
	if err != nil {
		return notification.NotificationID{}, fmt.Errorf("notification seeder: %s: %w", title, err)
	}
	return id, nil
}

// readReceiptExists reports whether the pair is already stamped.
func readReceiptExists(ctx context.Context, q datastore.Querier, noticeID notification.NotificationID, userID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(entity.TableNotificationReads).
		Where(sb.Equal("notification_id", strutils.ToUUID(noticeID)), sb.Equal("user_id", userID))

	query, args := sb.Build()
	var one int
	err := q.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
