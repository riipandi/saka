package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/responder"
)

// The failures the service defines. The handler maps them onto the codes
// the Connect protocol carries; the service defines what happened, not how
// it is answered.
var (
	// ErrNotFound is an identifier that names no notification.
	ErrNotFound = errors.New("notification: notification not found")

	// ErrInvalidAudience is an audience a category cannot carry: a system
	// notice with a topic or a global audience, or an audience that names
	// nobody.
	ErrInvalidAudience = errors.New("notification: the audience does not fit the category")

	// ErrUnknownTarget is an audience that names an account or a group the
	// deployment does not hold. The refusal names neither which one: the
	// caller's list is its own to keep straight.
	ErrUnknownTarget = errors.New("notification: the audience names an unknown account or group")
)

// Service carries the rules of the notifications: which audience a
// category may carry, who a notification reaches, and what a change
// records. The repository carries the SQL; the broker carries the live
// tail; the dispatcher carries the email pass.
type Service struct {
	pool   *datastore.Postgres
	repo   *Repository
	audit  *audit.Recorder
	log    *slog.Logger
	broker *Broker

	// dispatcher is the email pass. It is wired post-construction — the
	// queue exists by the time the area mounts, and the service must not
	// depend on it to construct. A nil dispatcher is an area served
	// without a queue: the notifications are in-app only.
	dispatcher *EmailDispatcher

	// now is the instant the service's decisions read. It is a field so a
	// test can hold the clock still.
	now func() time.Time
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:   pool,
		repo:   NewRepository(),
		audit:  recorder,
		log:    log,
		broker: NewBroker(),
		now:    time.Now,
	}
}

// WithEmailDispatcher wires the email pass post-construction. It answers
// the service, so the area's provider reads as one line.
func (s *Service) WithEmailDispatcher(d *EmailDispatcher) *Service {
	s.dispatcher = d
	return s
}

// Broker is the live tail the watch procedure subscribes to.
func (s *Service) Broker() *Broker { return s.broker }

// CreateParams carries the fields a notification is made of.
type CreateParams struct {
	Category string
	// Topic is the announcement's subject. A system notice refuses one.
	Topic string
	Title string
	Body  string
	// AudienceKind is the audience the notification targets. An
	// announcement that names none targets everyone; a system notice must
	// name one.
	AudienceKind string
	UserIDs      []uuid.UUID
	GroupIDs     []uuid.UUID
	// SendEmail asks the email pass for this notification. The
	// deployment's gate decides whether it runs.
	SendEmail bool
}

// Create publishes a notification to its audience. The rules the contract
// cannot express live here: which category carries which audience, and
// that the audience's members exist. The row, its junctions, and the
// record of the publication commit together; the live tail and the email
// pass ride after the commit, because a stream event a rollback
// contradicts is a lie a reader already saw.
func (s *Service) Create(ctx context.Context, creator uuid.UUID, params CreateParams) (Notification, error) {
	kind, err := s.resolveAudience(params)
	if err != nil {
		return Notification{}, err
	}
	if kind == AudienceUsers {
		err = s.ensureUsersExist(ctx, params.UserIDs)
		if err != nil {
			return Notification{}, err
		}
	}
	if kind == AudienceGroups {
		err = s.ensureGroupsExist(ctx, params.GroupIDs)
		if err != nil {
			return Notification{}, err
		}
	}

	row := Notification{
		ID:           uuid.NewV7(),
		Category:     params.Category,
		Topic:        topicOf(params.Topic),
		Title:        params.Title,
		Body:         params.Body,
		AudienceKind: kind,
		CreatedBy:    &creator,
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		createErr := s.repo.Create(ctx, tx, row, params.UserIDs, params.GroupIDs)
		if createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventNotificationCreated,
			Status:       audit.StatusSuccess,
			UserID:       creator.String(),
			ResourceType: ResourceNotification,
			Payload: map[string]string{
				"category":      params.Category,
				"audience_kind": kind,
				"send_email":    fmt.Sprintf("%t", params.SendEmail),
			},
		})
		return nil
	})
	if err != nil {
		return Notification{}, err
	}

	// The created row is read back so the answer and the live tail carry
	// what the database stored — the creation instant above all.
	stored, _, _, err := s.repo.Get(ctx, s.pool, row.ID)
	if err != nil {
		return Notification{}, err
	}

	s.publish(ctx, stored, params)
	if params.SendEmail && s.dispatcher != nil {
		s.dispatcher.EnqueueNotificationEmail(ctx, stored.ID)
	}
	return stored, nil
}

// CreateSystemNotice publishes the one notification the deployment itself
// writes: a message about an account's own object, addressed to that
// account alone. No creator is named — the system, not an operator, is the
// author — and no email pass is asked, because the notice is the inbox's
// business and the stream's convenience, not a message anyone composed.
func (s *Service) CreateSystemNotice(ctx context.Context, userID uuid.UUID, title, body string) error {
	row := Notification{
		ID:           uuid.NewV7(),
		Category:     CategorySystem,
		Title:        title,
		Body:         body,
		AudienceKind: AudienceUsers,
	}

	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if createErr := s.repo.Create(ctx, tx, row, []uuid.UUID{userID}, nil); createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventNotificationCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceNotification,
			Payload: map[string]string{
				"category":      CategorySystem,
				"audience_kind": AudienceUsers,
				"source":        "system",
			},
		})
		return nil
	})
	if err != nil {
		return err
	}

	stored, _, _, err := s.repo.Get(ctx, s.pool, row.ID)
	if err != nil {
		return err
	}
	s.publish(ctx, stored, CreateParams{
		Category:     CategorySystem,
		AudienceKind: AudienceUsers,
		UserIDs:      []uuid.UUID{userID},
	})
	return nil
}

// resolveAudience names the audience a create carries and refuses the
// pairs the contract cannot express: a system notice is about accounts,
// so it carries a topic never and a global audience never; an
// announcement defaults to everyone but may name its readers.
func (s *Service) resolveAudience(params CreateParams) (string, error) {
	if params.Category == CategorySystem {
		if params.Topic != "" {
			return "", ErrInvalidAudience
		}
		switch params.AudienceKind {
		case AudienceUsers:
			if len(params.UserIDs) == 0 {
				return "", ErrInvalidAudience
			}
			return AudienceUsers, nil
		case AudienceGroups:
			if len(params.GroupIDs) == 0 {
				return "", ErrInvalidAudience
			}
			return AudienceGroups, nil
		default:
			return "", ErrInvalidAudience
		}
	}

	switch params.AudienceKind {
	case "", AudienceGlobal:
		return AudienceGlobal, nil
	case AudienceUsers:
		if len(params.UserIDs) == 0 {
			return "", ErrInvalidAudience
		}
		return AudienceUsers, nil
	case AudienceGroups:
		if len(params.GroupIDs) == 0 {
			return "", ErrInvalidAudience
		}
		return AudienceGroups, nil
	default:
		return "", ErrInvalidAudience
	}
}

// ensureUsersExist refuses an audience that names an account the
// deployment does not hold. The read is the validation: one count against
// the distinct identifiers the request named.
func (s *Service) ensureUsersExist(ctx context.Context, ids []uuid.UUID) error {
	ok, err := s.countMatches(ctx, entity.TableUsers, ids)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnknownTarget
	}
	return nil
}

// ensureGroupsExist refuses an audience that names a group the deployment
// does not hold.
func (s *Service) ensureGroupsExist(ctx context.Context, ids []uuid.UUID) error {
	ok, err := s.countMatches(ctx, entity.TableUserGroups, ids)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnknownTarget
	}
	return nil
}

// countMatches answers whether every identifier names a row of the table.
func (s *Service) countMatches(ctx context.Context, table string, ids []uuid.UUID) (bool, error) {
	targets := make([]any, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, id)
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(distinct id)")
	sb.From(table)
	sb.Where(sb.In("id", targets...))

	query, args := sb.Build()
	var found int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("notification: validate audience: %w", err)
	}
	return found == len(ids), nil
}

// Get answers one notification's full view, with the audiences its kind
// names.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Notification, []uuid.UUID, []uuid.UUID, error) {
	row, userIDs, groupIDs, err := s.repo.Get(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return Notification{}, nil, nil, ErrNotFound
	}
	if err != nil {
		return Notification{}, nil, nil, err
	}
	return row, userIDs, groupIDs, nil
}

// ListAll answers one page of every notification, ordered as the caller
// asked (absent a choice, newest first).
func (s *Service) ListAll(ctx context.Context, category, sortBy string, ascending bool, page, limit int) ([]Notification, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListAll(ctx, s.pool, category, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return rows, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// Cancel withdraws one notification. The row is read first, so an unknown
// identifier is the not-found failure and a second cancellation is the
// success the state already names; the record commits with the stamp.
func (s *Service) Cancel(ctx context.Context, admin uuid.UUID, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, _, _, err := s.repo.Get(ctx, tx, id); err != nil {
			if errors.Is(err, datastore.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		cancelled, err := s.repo.Cancel(ctx, tx, id, s.now())
		if err != nil {
			return err
		}
		if !cancelled {
			return nil
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventNotificationCancelled,
			Status:       audit.StatusSuccess,
			UserID:       admin.String(),
			ResourceType: ResourceNotification,
			ResourceID:   id.String(),
		})
		return nil
	})
}

// ListInbox answers one page of the caller's inbox, ordered by creation
// newest first until the request sorts otherwise.
func (s *Service) ListInbox(ctx context.Context, userID uuid.UUID, unreadOnly bool, category, sortBy string, ascending bool, page, limit int) ([]InboxRow, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListInbox(ctx, s.pool, userID, unreadOnly, category, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return rows, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// MarkRead writes the caller's receipt for one notification. A
// notification the caller is not targeted by answers the not-found the
// whole account surface answers — the receipt is never written for a
// notification the caller cannot see.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		visible, err := s.repo.IsVisible(ctx, tx, id, userID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrNotFound
		}
		return s.repo.InsertRead(ctx, tx, id, userID)
	})
}

// MarkAllRead writes the caller's missing receipts across their whole
// inbox, and answers how many it marked.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error) {
	var marked int64
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		count, err := s.repo.MarkAllRead(ctx, tx, userID)
		marked = count
		return err
	})
	return marked, err
}

// UnreadCount answers the number the bell badge shows.
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.repo.UnreadCount(ctx, s.pool, userID)
}

// Subscribe registers one live-tail stream for the account.
func (s *Service) Subscribe(userID uuid.UUID) (<-chan Notification, func()) {
	return s.broker.Subscribe(userID)
}

// publish hands a created notification to the live tails it reaches: the
// global audience to every open stream, a named one to the accounts it
// names. The member resolution is a read after the commit — a member the
// group lost between commit and publish misses the stream and catches up
// through the list, which is the catch-up the stream promises.
func (s *Service) publish(ctx context.Context, row Notification, params CreateParams) {
	switch row.AudienceKind {
	case AudienceGlobal:
		s.broker.PublishAll(row)
	case AudienceUsers:
		s.broker.PublishTo(params.UserIDs, row)
	case AudienceGroups:
		ids, err := s.repo.GroupMemberUserIDs(ctx, s.pool, params.GroupIDs)
		if err != nil {
			s.log.Warn("notification: live tail skipped", "error", err)
			return
		}
		s.broker.PublishTo(ids, row)
	}
}

// topicOf turns an absent topic into the NULL its column stores.
func topicOf(topic string) *string {
	if topic == "" {
		return nil
	}
	return &topic
}
