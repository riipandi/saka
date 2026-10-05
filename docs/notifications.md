# Notifications

Notifications are Saka's in-product message board: announcements to
everyone, notices to a group, and personal messages to one account —
with read receipts and a live stream.

## The model

| Concept | Behavior |
| --- | --- |
| **Audience** | `global` (everyone), a list of users, or a list of groups — resolved when the reader asks, never fanned out at creation, so a user added to a group later still sees the group's notices |
| **System notices** | Target one account, carry no topic and no global audience — these are the messages the system itself leaves (a recovery-code warning, an email-change pending notice) |
| **Topic** | A label a reader can filter by |
| **Read state** | One receipt per account per notification — marking read is personal, never global |
| **Live updates** | A server-streaming watch: the client keeps one connection open and hears new notifications as they are created |

## Reading and managing

A signed-in user lists their own notifications, counts the unread ones,
marks one or all as read, and streams new arrivals. An administrator
creates, inspects, cancels, and lists everything — including who has
read what.

## The email companion

Announcements can also send an email fan-out; a retried or duplicated
creation is the one place a repeat could double-send — client-side
deduplication applies until idempotency keys land (a known, deferred
improvement; see [API endpoints](api-endpoint.md) "Future Improvements").

The machine-facing surface: [API endpoints](api-endpoint.md#notifications).
