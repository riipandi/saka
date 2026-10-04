package scimsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/riipandi/saka/framework/fetcher"
)

// remoteUser is one account's SCIM 2.0 document, the fields the sync
// writes and reads back.
type remoteUser struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id,omitempty"`
	ExternalID  string        `json:"externalId,omitempty"`
	UserName    string        `json:"userName"`
	Active      bool          `json:"active"`
	Name        *remoteName   `json:"name,omitempty"`
	DisplayName string        `json:"displayName,omitempty"`
	Emails      []remoteEmail `json:"emails,omitempty"`
	Meta        *remoteMeta   `json:"meta,omitempty"`
}

type remoteName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type remoteEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
}

// remoteGroupRef is one group member's reference. The value is the
// remote's own user id, not the local one.
type remoteGroupRef struct {
	Value string `json:"value"`
}

// remoteGroup is one group's SCIM 2.0 document.
type remoteGroup struct {
	Schemas     []string         `json:"schemas"`
	ID          string           `json:"id,omitempty"`
	ExternalID  string           `json:"externalId,omitempty"`
	DisplayName string           `json:"displayName"`
	Members     []remoteGroupRef `json:"members,omitempty"`
	Meta        *remoteMeta      `json:"meta,omitempty"`
}

// remoteMeta is the resource's metadata block. The timestamps fence a
// pointless write: a row the local side has not touched since the remote's
// last change is left alone.
type remoteMeta struct {
	Created      *time.Time `json:"created,omitempty"`
	LastModified *time.Time `json:"lastModified,omitempty"`
}

// remoteList is the SCIM list-response wrapper, decoded with presence
// detection: the pointer fields tell a missing member apart from a zero
// one, so a body that is not a well-formed SCIM list cannot decode into
// the empty snapshot the reconcile would act on. `Resources` is
// capitalized per RFC 7644 §3.4.2, the one member the spec spells in
// title case.
type remoteList[T any] struct {
	Schemas      []string `json:"schemas,omitempty"`
	TotalResults *int     `json:"totalResults"`
	StartIndex   *int     `json:"startIndex,omitempty"`
	ItemsPerPage *int     `json:"itemsPerPage,omitempty"`
	Resources    *[]T     `json:"Resources"`
}

// pathEscape makes a remote id safe in a URL path segment.
func pathEscape(raw string) string { return url.PathEscape(raw) }

// reconcile pushes the snapshot onto the remote: users first, so the
// groups' member references exist; groups second. Per-row failures are
// joined — one account the remote refuses must not stop the pass.
// Before any write, the listings are validated and indexed: a remote
// row without its identifiers makes the snapshot unusable, and the pass
// fails without touching the remote.
func (s *Service) reconcile(ctx context.Context, snap snapshot, stats *Stats) error {
	remoteUsers, err := s.listRemoteUsers(ctx, snap)
	if err != nil {
		return err
	}
	remoteGroups, err := s.listRemoteGroups(ctx, snap)
	if err != nil {
		return err
	}
	if err := validateRemoteSnapshot(remoteUsers, remoteGroups); err != nil {
		return err
	}

	var errs []error

	// The pass matches by externalId: one index per resource kind, built
	// once, keeps the reconcile at O(local + remote) lookups. The first
	// row wins a duplicate externalId — the listing order is the
	// remote's, so the choice is deterministic.
	userIndex := indexByExternalID(remoteUsers, func(r *remoteUser) string { return r.ExternalID })
	groupIndex := indexByExternalID(remoteGroups, func(r *remoteGroup) string { return r.ExternalID })

	// The groups' members reference the remote user ids, so the pass
	// carries the mapping from the local id to the remote one: the
	// listing's rows for what already exists, the creates' answers for
	// what the pass just makes.
	remoteUserID := make(map[string]string, len(userIndex))
	for externalID, row := range userIndex {
		remoteUserID[externalID] = row.ID
	}

	// Push the local accounts, then remove the remote rows the snapshot no
	// longer names.
	localUsers := make(map[string]struct{}, len(snap.users))
	for _, u := range snap.users {
		localUsers[u.ID.String()] = struct{}{}
		existing := userIndex[u.ID.String()]
		remoteID, err := s.pushUser(ctx, snap, u, existing)
		if err != nil {
			errs = append(errs, fmt.Errorf("user %s: %w", u.ID, err))
			continue
		}
		if existing == nil {
			stats.UsersCreated++
		} else {
			stats.UsersUpdated++
		}
		if remoteID != "" {
			remoteUserID[u.ID.String()] = remoteID
		}
	}
	for _, r := range remoteUsers {
		if _, local := localUsers[r.ExternalID]; local {
			continue
		}
		if err := s.deleteRemote(ctx, snap, "/Users/"+pathEscape(r.ID)); err != nil {
			errs = append(errs, fmt.Errorf("delete user %s: %w", r.ExternalID, err))
			continue
		}
		stats.UsersDeleted++
	}

	localGroups := make(map[string]struct{}, len(snap.groups))
	for _, g := range snap.groups {
		localGroups[g.ID.String()] = struct{}{}
		existing := groupIndex[g.ID.String()]
		if err := s.pushGroup(ctx, snap, g, existing, remoteUserID, stats); err != nil {
			errs = append(errs, fmt.Errorf("group %s: %w", g.ID, err))
		}
	}
	for _, r := range remoteGroups {
		if _, local := localGroups[r.ExternalID]; local {
			continue
		}
		if err := s.deleteRemote(ctx, snap, "/Groups/"+pathEscape(r.ID)); err != nil {
			errs = append(errs, fmt.Errorf("delete group %s: %w", r.ExternalID, err))
			continue
		}
		stats.GroupsDeleted++
	}

	return errors.Join(errs...)
}

// validateRemoteSnapshot refuses a listing whose rows lack the
// identifiers the reconcile works with: the remote id a delete targets
// and the externalId the match is keyed by. Saka provisions every
// resource with an externalId, so a row without one is not a row the
// snapshot contract describes — the pass fails before any write rather
// than deleting what it cannot read.
func validateRemoteSnapshot(users []remoteUser, groups []remoteGroup) error {
	var errs []error
	for i := range users {
		if users[i].ID == "" || users[i].ExternalID == "" {
			errs = append(errs, fmt.Errorf("remote user row %d carries no id or externalId", i+1))
		}
	}
	for i := range groups {
		if groups[i].ID == "" || groups[i].ExternalID == "" {
			errs = append(errs, fmt.Errorf("remote group row %d carries no id or externalId", i+1))
		}
	}
	return errors.Join(errs...)
}

// indexByExternalID maps one resource kind's rows by their externalId,
// preserving first-wins on duplicates for deterministic matching.
func indexByExternalID[T any](rows []T, keyOf func(*T) string) map[string]*T {
	index := make(map[string]*T, len(rows))
	for i := range rows {
		key := keyOf(&rows[i])
		if _, seen := index[key]; !seen {
			index[key] = &rows[i]
		}
	}
	return index
}

// pushUser creates or updates one account on the remote, and answers the
// remote's own id — the one the group members reference. An untouched row
// answers the empty string: no write happened, and the listing's mapping
// already carries it.
func (s *Service) pushUser(ctx context.Context, snap snapshot, u ProvisionedUser, existing *remoteUser) (string, error) {
	if existing == nil {
		var created remoteUser
		if err := s.scimCall(ctx, snap, http.MethodPost, "/Users", userPayload(u), &created); err != nil {
			return "", err
		}
		return created.ID, nil
	}

	// The remote's lastModified fences a pointless write: a row neither
	// side has touched since the last pass is left alone.
	if u.UpdatedAt != nil && existing.Meta != nil && existing.Meta.LastModified != nil &&
		u.UpdatedAt.Before(*existing.Meta.LastModified) {
		return "", nil
	}

	var updated remoteUser
	if err := s.scimCall(ctx, snap, http.MethodPut, "/Users/"+pathEscape(existing.ID), userPayload(u), &updated); err != nil {
		return "", err
	}
	return existing.ID, nil
}

// pushGroup creates or updates one group. The members reference the remote
// user ids the user half of the pass recorded.
func (s *Service) pushGroup(ctx context.Context, snap snapshot, g ProvisionedGroup, existing *remoteGroup, remoteUserID map[string]string, stats *Stats) error {
	members := make([]remoteGroupRef, 0, len(g.MemberIDs))
	for _, id := range g.MemberIDs {
		remote, ok := remoteUserID[id.String()]
		if !ok {
			// The user half already pushed every visible account, so a
			// missing row is an upstream refusal; the group is skipped
			// this pass rather than written half-blind.
			return fmt.Errorf("member %s is not provisioned on the remote", id)
		}
		members = append(members, remoteGroupRef{Value: remote})
	}

	payload := remoteGroup{
		Schemas:     []string{scimGroupSchema},
		ExternalID:  g.ID.String(),
		DisplayName: g.DisplayName,
		Members:     members,
	}

	if existing == nil {
		var created remoteGroup
		if err := s.scimCall(ctx, snap, http.MethodPost, "/Groups", payload, &created); err != nil {
			return err
		}
		stats.GroupsCreated++
		return nil
	}
	if g.UpdatedAt != nil && existing.Meta != nil && existing.Meta.LastModified != nil &&
		g.UpdatedAt.Before(*existing.Meta.LastModified) {
		return nil
	}
	var updated remoteGroup
	if err := s.scimCall(ctx, snap, http.MethodPut, "/Groups/"+pathEscape(existing.ID), payload, &updated); err != nil {
		return err
	}
	stats.GroupsUpdated++
	return nil
}

// deleteRemote removes one remote row. A 404 answers nil — the row the
// pass meant to remove is already gone, which is the state it wanted.
func (s *Service) deleteRemote(ctx context.Context, snap snapshot, p string) error {
	target := scimURL(snap.provider.Endpoint, p, nil)
	res, err := s.httpClient.Do(ctx, fetcher.Request{
		Method: http.MethodDelete,
		URL:    target,
		Headers: http.Header{
			"Accept":        []string{scimContentType},
			"Authorization": []string{"Bearer " + snap.token},
		},
	})
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	return s.readSCIMAnswer(res, nil, target)
}

// scimURL joins a provider's base endpoint with a resource path and a
// query. The resource path arrives with its ids already escaped
// (pathEscape), so the join is plain concatenation: routing an escaped
// segment through url.URL's Path field would re-encode the percent
// signs, and url.URL cannot represent an encoded slash inside one
// segment at all. The base's own path — a deployment behind a prefix —
// is preserved as the operator wrote it.
func scimURL(endpoint, p string, query url.Values) string {
	if _, err := url.Parse(endpoint); err != nil {
		return endpoint + p
	}
	target := strings.TrimSuffix(endpoint, "/") + p
	if query != nil {
		target += "?" + query.Encode()
	}
	return target
}

// userPayload renders one account's SCIM document.
func userPayload(u ProvisionedUser) remoteUser {
	payload := remoteUser{
		Schemas:     []string{scimUserSchema},
		ExternalID:  u.ID.String(),
		UserName:    u.Username,
		Active:      u.Active,
		DisplayName: u.DisplayName,
		Name: &remoteName{
			GivenName:  u.FirstName,
			FamilyName: u.LastName,
		},
	}
	if u.Email != "" {
		payload.Emails = []remoteEmail{{Value: u.Email, Primary: true}}
	}
	return payload
}

// listRemoteUsers walks the remote's user pages with SCIM pagination, so
// a large provider loses nothing.
func (s *Service) listRemoteUsers(ctx context.Context, snap snapshot) ([]remoteUser, error) {
	rows, err := listRemote[remoteUser](ctx, s, snap, "/Users")
	if err != nil {
		return nil, fmt.Errorf("list remote users: %w", err)
	}
	return rows, nil
}

// listRemoteGroups walks the remote's group pages.
func (s *Service) listRemoteGroups(ctx context.Context, snap snapshot) ([]remoteGroup, error) {
	rows, err := listRemote[remoteGroup](ctx, s, snap, "/Groups")
	if err != nil {
		return nil, fmt.Errorf("list remote groups: %w", err)
	}
	return rows, nil
}

// listRemote pages one resource type until the remote says it is done.
// Every page is validated before it joins the snapshot: a body that is
// not a well-formed SCIM list, negative or contradictory counts, or a
// remote that never advances the page cursor fails the listing. The
// reconcile behind the listing deletes what the snapshot does not name,
// so an untrusted page must never be able to read as an empty or
// partial one — a malformed listing fails the pass, it never truncates.
func listRemote[T any](ctx context.Context, s *Service, snap snapshot, basePath string) ([]T, error) {
	var all []T
	startIndex := 1
	for page := 1; ; page++ {
		if page > scimMaxPages {
			return nil, fmt.Errorf("remote %s listing exceeded %d pages", basePath, scimMaxPages)
		}

		var decoded remoteList[T]
		query := url.Values{
			"startIndex": {strconv.Itoa(startIndex)},
			"count":      {strconv.Itoa(scimPageCount)},
		}
		if err := s.scimCallQuery(ctx, snap, http.MethodGet, basePath, query, nil, &decoded); err != nil {
			return nil, err
		}
		if decoded.TotalResults == nil || decoded.Resources == nil {
			return nil, fmt.Errorf("remote %s page %d is not a well-formed SCIM list response", basePath, page)
		}
		total, resources := *decoded.TotalResults, *decoded.Resources
		if total < 0 || total < len(resources) {
			return nil, fmt.Errorf("remote %s page %d reports %d total results against %d rows", basePath, page, total, len(resources))
		}
		if len(resources) == 0 {
			if total > len(all) {
				return nil, fmt.Errorf("remote %s page %d stopped serving pages after %d of %d results", basePath, page, len(all), total)
			}
			return all, nil
		}
		if len(all)+len(resources) > scimMaxResources {
			return nil, fmt.Errorf("remote %s listing exceeded %d resources", basePath, scimMaxResources)
		}
		all = append(all, resources...)
		if len(all) >= total {
			return all, nil
		}
		if decoded.ItemsPerPage == nil || *decoded.ItemsPerPage <= 0 {
			return nil, fmt.Errorf("remote %s page %d carries no page size to advance by", basePath, page)
		}
		startIndex += *decoded.ItemsPerPage
	}
}

// scimCall performs one SCIM request without a query string, decoding the
// answer body when out is not nil.
func (s *Service) scimCall(ctx context.Context, snap snapshot, method, p string, payload any, out any) error {
	return s.scimCallQuery(ctx, snap, method, p, nil, payload, out)
}

// scimCallQuery performs one SCIM request. The token rides the
// Authorization header and is never logged; a 429 retries with the
// server's hint or a capped backoff; any other status is the error the
// body names.
func (s *Service) scimCallQuery(ctx context.Context, snap snapshot, method, p string, query url.Values, payload any, out any) error {
	endpoint, err := url.Parse(snap.provider.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid scim endpoint: %w", err)
	}
	endpoint.Path = path.Join(strings.TrimRight(endpoint.Path, "/"), p)
	endpoint.RawQuery = query.Encode()
	target := endpoint.String()

	var body []byte
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode scim payload: %w", err)
		}
	}

	const attempts = 3
	for attempt := 1; ; attempt++ {
		res, err := s.httpClient.Do(ctx, fetcher.Request{
			Method: method,
			URL:    target,
			Query:  query,
			Headers: http.Header{
				"Accept":        []string{scimContentType},
				"Content-Type":  []string{scimContentType},
				"Authorization": []string{"Bearer " + snap.token},
			},
			Body: body,
		})
		if err != nil {
			return err
		}

		if res.StatusCode == http.StatusTooManyRequests && attempt < attempts {
			delay := retryDelay(res.Header, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		return s.readSCIMAnswer(res, out, target)
	}
}

// retryDelay reads the server's Retry-After when it offers one, a capped
// exponential backoff when it does not.
func retryDelay(header http.Header, attempt int) time.Duration {
	if header != nil {
		if raw := header.Get("Retry-After"); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
			if at, err := http.ParseTime(raw); err == nil {
				if wait := time.Until(at); wait > 0 {
					return wait
				}
			}
		}
	}
	maxDelay := 10 * time.Second
	delay := 500 * time.Millisecond << (attempt - 1)
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

// readSCIMAnswer turns one response into a decoded answer or an error. The
// body is bounded, so a hostile upstream cannot exhaust the reader; the
// error names the status and the body's first line, and the body is
// trimmed of anything an operator should not paste into a ticket.
func (s *Service) readSCIMAnswer(res *fetcher.Response, out any, target string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if out == nil || len(res.Body) == 0 {
			return nil
		}
		if err := json.Unmarshal(res.Body, out); err != nil {
			return fmt.Errorf("decode scim answer from %s: %w", target, err)
		}
		return nil
	}

	body := strings.TrimSpace(string(res.Body))
	if len(body) > scimErrorBodyLimit {
		body = body[:scimErrorBodyLimit]
	}
	return fmt.Errorf("scim request to %s failed with status %d: %s", target, res.StatusCode, body)
}
