# Work item field and collaboration reference

A work item is the unit of delivery work under a feature. Every work item
belongs to exactly one project and has a project-scoped key
(`<project key>-<item number>`). The same aggregate is rendered by the board
drawer, planning views and search deep links.

## Fields

| Field | Type | Rules |
|---|---|---|
| `key` | string | Derived from the project key and `item_number`; never client-supplied |
| `type` | enum | `story`, `task`, `subtask`, `bug`, `spike`, `chore` |
| `title` | string | Required, trimmed |
| `description` | text | Plain text |
| `acceptance_criteria` | text | Plain text |
| `status` | enum | Workflow depends on `type`; only documented transitions are accepted |
| `priority` | enum | `low`, `medium`, `high`, `critical` |
| `severity` | enum or null | Bugs: `blocker`, `critical`, `major`, `minor`, `trivial` |
| `feature_id` | UUID or null | Required for stories, tasks and subtasks; bugs need a feature or parent |
| `parent_id` | UUID or null | Self-parenting is rejected by a database check and the API |
| `sprint_id`, `release_id`, `environment_id` | UUID or null | Must belong to the same project |
| `assignee_id`, `reporter_id` | UUID or null | Must be project members |
| `estimate_points` | number or null | Non-negative |
| `due_date` | date or null | ISO date |
| `reproduction_steps`, `expected_result`, `actual_result` | text | Defect evidence |
| `labels` | string[] | At most 10, each at most 24 characters; lower-case letters, digits, spaces, hyphens and underscores |
| `revision` | integer | Optimistic concurrency token owned by the database |

Labels are normalized (trimmed, lower-cased, de-duplicated) before they are
stored. Migration `017` adds `work_items.labels` with a GIN index, so the board
can filter by containment without a join table.

## Optimistic concurrency

Create and update accept the standard planning revision token (`If-Match`,
`expected_version` query, or `expected_version` body). Missing, stale and
malformed tokens return `428`, `409` and `400` respectively, as documented in
[`API.md`](API.md). Collaboration sub-resources (comments, watchers,
attachments, links) are not revisioned; they are individually identified and
permission-checked.

## Detail endpoint

`GET /api/v1/projects/{id}/work-items/{itemId}` returns the work item plus the
collections used by the drawer:

- `children` and `child_count`
- `comments` with resolved `mentions` and `edited_at`
- `watchers`, `watcher_count` and `watching` for the caller
- `attachments` (scan status, checksum, uploader) and `attachment_count`
- `links` in both directions with the other artifact's key, title and status
- `activity` (latest 100 events), `branches` and `pull_requests`

## Comments and mentions

- `POST /api/v1/projects/{id}/work-items/{itemId}/comments`
- `PATCH /api/v1/projects/{id}/comments/{commentId}`
- `DELETE /api/v1/projects/{id}/comments/{commentId}`

Bodies are plain text: CR/LF is normalized, control characters are stripped and
the length is capped at 10,000 characters. `@username` mentions are resolved
against current project members and stored in `comment_mentions`; usernames that
are not members are stored as literal text and do not notify anyone. Clients
must render comment bodies escaped (Vue interpolation does this by default).
Only the author or a project owner/maintainer may edit or delete a comment.

## Watchers

- `POST /api/v1/projects/{id}/work-items/{itemId}/watch` → `{"watching": true}`
- `DELETE /api/v1/projects/{id}/work-items/{itemId}/watch`

Watching is idempotent. Watchers receive the same activity/audit evidence as
other collaboration mutations.

## Links

- `POST /api/v1/projects/{id}/work-items/{itemId}/links`
- `DELETE /api/v1/projects/{id}/work-items/{itemId}/links/{linkId}`

Linkable artifact types are an allowlist: `prd`, `epic`, `feature`,
`work_item`, `test_case`, `sprint`, `release`. The target must belong to the
same project, and a work item cannot link to itself. The link tuple is unique,
so repeating a link is idempotent. The detail aggregate returns inbound and
outbound links, which makes the relationship visible from both artifacts.

## Attachments

- `POST /api/v1/projects/{id}/work-items/{itemId}/attachments` (multipart field `file`)
- `GET /api/v1/projects/{id}/attachments/{attachmentId}/download`
- `DELETE /api/v1/projects/{id}/attachments/{attachmentId}`

Policy:

- **Size:** `ATTACHMENT_MAX_BYTES`, default 10 MiB per file.
- **Allowed types:** PNG, JPEG, GIF, WebP, PDF, plain text, log, Markdown, CSV,
  JSON and ZIP. The extension, MIME type and magic bytes must agree.
- **Rejected types:** HTML, SVG, XML, scripts, executables, archives other than
  ZIP, and text that contains active markup (`<script>`, `<html>`, `<svg>`,
  `<iframe>`, `javascript:`).
- **Malware:** a built-in signature check rejects the EICAR test file. Setting
  `CLAMAV_ADDR` enables ClamAV `INSTREAM` scanning; if the configured daemon is
  unreachable the upload fails closed with `503 SCAN_UNAVAILABLE`.
- **Storage:** files are stored under `ATTACHMENTS_DIR` using the generated
  attachment UUID, never the client-supplied filename. Path traversal is
  rejected on read and delete as well.
- **Permissions:** upload/delete require `work_item.write`; download requires
  project read. Only clean scans are served, with `nosniff`, a sandbox CSP and
  `Content-Disposition: attachment`.
- **Audit:** each upload attempt (including rejection reasons), download and
  delete is recorded in the append-only audit log. Uploads and deletes also emit
  an activity event.

The Compose stack persists attachments in the `attachments_data` volume mounted
at `/data/attachments`.

## Activity, audit and realtime

Every structural mutation emits one `activity_events` row and publishes a
`workspace.changed` NATS event. Connected clients receive the SSE event and
refresh the workspace and the open drawer. Security-relevant mutations also
write an audit entry through the GOV-04 writer; audit failures never fail the
business request.

## Deep links and keyboard behavior

- `/projects/{projectId}/workspace?tab=board&item={workItemId}` opens the drawer.
- Board cards, search results, planning links and traceability links navigate to
  the same URL, so the browser back/forward buttons close and reopen the drawer.
- `Esc` closes the drawer, focus returns to the element that opened it, `Tab` is
  trapped inside the panel, and the six tabs support `ArrowLeft`,
  `ArrowRight`, `Home` and `End`.
- `Ctrl/Cmd+Enter` submits a comment; `@` opens the member autocomplete with
  arrow-key selection and `Enter`/`Tab` to accept.
