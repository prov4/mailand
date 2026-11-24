# Technical Specification & Agent Handoff: Mailand, a Hyprland-Native PIM Suite (`mailand`)

## 1. Project Overview & Goals
Build a modern, fast, Wayland-native Personal Information Manager (Email, Calendar, Contacts) designed specifically for **Hyprland** (tiling Wayland compositor), replacing GNOME Evolution with **zero GNOME, GLib, or GTK dependencies**.

### Core Design Principles
- **Strict Process & Language Separation:**
  - **Backend (`mailand-daemon`):** Written in **Go**, running headless as a `systemd --user` background service. Owns all network protocols (IMAP, SMTP, CalDAV, CardDAV), local storage (SQLite + FTS5), full-text indexing, and credential retrieval.
  - **Frontend (`mailand-ui`):** Written in **C++20 + Qt 6 (Qt Quick / QML)**, running natively on Wayland (`QT_QPA_PLATFORM=wayland`). Acts purely as a thin, keyboard-first view layer.
  - **IPC Bridge:** **gRPC (Protobuf)** communicating over a local Linux Unix Domain Socket (`/run/user/$UID/mailand.sock`). Never use `cgo` to link Go and C++ directly.
- **Off-the-Shelf Protocol & Rendering Engines:** Do not reinvent protocol parsers or HTML renderers.
  - Use the `emersion` Go ecosystem (`go-imap/v2`, `go-message`, `go-smtp`, `go-webdav`) for all mail and DAV protocols.
  - Use **Qt WebEngine (`WebEngineView`)** in QML for sandboxed HTML email rendering.
- **Hyprland-First UX:** Frameless window (`Qt.FramelessWindowHint`), zero redundant titlebars/decorations, Vim-inspired keyboard navigation (`j`/`k`, `/`, `c`, `r`, `dd`), and clean dark-mode/themeable QML styling.

---

## 2. Technology Stack & Dependencies

### Backend (`backend/` -> binary: `mailand-daemon`)
- **Language:** Go 1.23+
- **Mail Protocols:** `github.com/emersion/go-imap/v2`, `github.com/emersion/go-message`, `github.com/emersion/go-smtp`
- **Calendar & Contacts:** `github.com/emersion/go-webdav` (CalDAV & CardDAV)
- **Database & Search:** `modernc.org/sqlite` (pure Go SQLite driver) or `github.com/mattn/go-sqlite3` compiled with `-tags "fts5"`
- **Secrets / Keyring:** `github.com/zalando/go-keyring` (communicates via `org.freedesktop.secrets` D-Bus)
- **IPC:** `google.golang.org/grpc`, `google.golang.org/protobuf`
- **OAuth2 (Gmail):** `golang.org/x/oauth2` (+ `golang.org/x/oauth2/google` endpoints), `github.com/emersion/go-sasl` (OAUTHBEARER; small custom XOAUTH2 client if needed)

### Frontend (`frontend/` -> binary: `mailand-ui`)
- **Language:** C++20, CMake 3.24+ (chosen over qmake for reliable gRPC/Protobuf codegen and WebEngine linking)
- **Packaging:** Arch `PKGBUILD` in `deploy/` (installs both binaries, systemd user unit, and `.desktop` launcher entry)
- **UI Framework:** Qt 6 (`Core`, `Gui`, `Qml`, `Quick`, `QuickControls2`, `WaylandClient`)
- **HTML Email Renderer:** Qt 6 `WebEngineQuick` (`QtWebEngine`)
- **IPC:** C++ `gRPC` + `Protobuf` (connecting to `unix:///run/user/<uid>/mailand.sock`)

---

## 3. Repository Structure

```text
mailand/
├── Makefile                        # Unified build, proto-gen, and install targets
├── proto/
│   └── mailand.proto                   # Single source of truth for IPC contract
├── backend/
│   ├── go.mod
│   ├── cmd/
│   │   └── mailand-daemon/
│   │       └── main.go             # Entrypoint, signal handling, socket listener
│   └── internal/
│       ├── config/                 # TOML config loader (~/.config/mailand/config.toml)
│       ├── store/                  # SQLite schema, migrations, FTS5 search queries
│       ├── mail/                   # IMAP sync worker, IDLE watcher, MIME parser, SMTP sender
│       ├── dav/                    # CalDAV & CardDAV sync worker (.ics / .vcf)
│       └── rpc/                    # gRPC server implementation & event pub/sub stream
├── frontend/
│   ├── CMakeLists.txt
│   ├── src/
│   │   ├── main.cpp                # Wayland setup, QML engine initialization
│   │   ├── RpcClient.h/.cpp        # Async gRPC client & server-stream event listener
│   │   ├── FolderModel.h/.cpp      # QAbstractListModel for accounts & folders
│   │   ├── MessageModel.h/.cpp     # Paginated QAbstractListModel for message headers
│   │   ├── MessageView.h/.cpp      # Q_OBJECT exposing current message body/metadata to QML
│   │   └── MailUrlInterceptor.h/.cpp # QWebEngineUrlRequestInterceptor blocking remote trackers
│   └── qml/
│       ├── Main.qml                # Root frameless ApplicationWindow + global keybindings
│       ├── Theme.qml               # Singleton color/spacing/typography tokens
│       ├── SidebarPane.qml         # Accounts, folders, and view switcher (Mail/Cal/Contacts)
│       ├── MessageListPane.qml     # Virtualized ListView with search bar
│       ├── ReaderPane.qml          # Header metadata, remote-image banner, MailViewer
│       ├── MailViewer.qml          # Locked-down WebEngineView component
│       └── ComposerModal.qml       # Keyboard-driven email composer
└── deploy/
    ├── mailand-daemon.service          # systemd user unit file
    ├── mailand-ui.desktop              # App launcher entry (StartupWMClass=mailand-ui)
    └── PKGBUILD                    # Arch package: mailand-daemon, mailand-ui, unit, .desktop

```

---

## 4. IPC Contract (proto/mailand.proto)
Implement the following baseline Protobuf schema first so backend and frontend can be developed and tested independently:

```proto
syntax = "proto3";

package mailand.v1;
option go_package = "mailand/backend/internal/rpc/pb;pb";

service MailandService {
  // Accounts & Folders
  rpc ListAccounts(Empty) returns (AccountList);
  rpc ListFolders(AccountRequest) returns (FolderList);

  // Mail Operations
  rpc ListMessages(ListMessagesRequest) returns (MessageHeaderList);
  rpc GetMessage(MessageRequest) returns (MessageDetail);
  rpc SearchMessages(SearchRequest) returns (MessageHeaderList);
  rpc SetMessageFlags(SetFlagsRequest) returns (Empty);
  rpc SendMessage(SendMessageRequest) returns (Empty);
  rpc TriggerSync(AccountRequest) returns (Empty);

  // Real-time Push Events (New mail, sync status, flag updates)
  rpc SubscribeEvents(Empty) returns (stream DaemonEvent);
}

message Empty {}

message AccountRequest {
  string account_id = 1;
}

message Account {
  string id = 1;
  string name = 2;
  string email = 3;
}

message AccountList {
  repeated Account accounts = 1;
}

message Folder {
  string id = 1;
  string account_id = 2;
  string name = 3;
  string path = 4;
  uint32 unread_count = 5;
}

message FolderList {
  repeated Folder folders = 1;
}

message ListMessagesRequest {
  string folder_id = 1;
  uint32 limit = 2;
  uint32 offset = 3;
}

message MessageHeader {
  string id = 1;
  string folder_id = 2;
  uint32 uid = 3;
  string subject = 4;
  string from_name = 5;
  string from_address = 6;
  int64 timestamp_unix = 7;
  bool is_read = 8;
  bool is_flagged = 9;
  bool has_attachments = 10;
  string snippet = 11;
  repeated string folder_ids = 12; // all folders/labels this message appears in
}

message MessageHeaderList {
  repeated MessageHeader messages = 1;
  uint32 total_count = 2;
}

message MessageRequest {
  string message_id = 1;
}

message AttachmentInfo {
  string id = 1;
  string filename = 2;
  string mime_type = 3;
  uint64 size_bytes = 4;
}

message MessageDetail {
  MessageHeader header = 1;
  repeated string to_addresses = 2;
  repeated string cc_addresses = 3;
  string text_body = 4;
  string html_body = 5;
  repeated AttachmentInfo attachments = 6;
}

message SearchRequest {
  string query = 1;
  string account_id = 2; // Optional filter
  uint32 limit = 3;
}

message SetFlagsRequest {
  repeated string message_ids = 1;
  optional bool is_read = 2;
  optional bool is_flagged = 3;
}

message SendMessageRequest {
  string account_id = 1;
  repeated string to = 2;
  repeated string cc = 3;
  string subject = 4;
  string text_body = 5;
  string html_body = 6;
  string in_reply_to = 7;
}

message DaemonEvent {
  enum Type {
    SYNC_STARTED = 0;
    SYNC_FINISHED = 1;
    NEW_MESSAGES = 2;
    FLAGS_UPDATED = 3;
    ERROR = 4;
  }
  Type type = 1;
  string account_id = 2;
  string folder_id = 3;
  string detail = 4;
}
```

---


## 5. Database Schema (backend/internal/store/schema.sql)
SQLite (~/.local/share/mailand/mailand.db) configured with PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;:

```sql
CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    imap_host TEXT NOT NULL,
    imap_port INTEGER NOT NULL,
    smtp_host TEXT NOT NULL,
    smtp_port INTEGER NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'password',  -- 'password' | 'oauth2_google'
    caldav_url TEXT,
    carddav_url TEXT
);

CREATE TABLE IF NOT EXISTS folders (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,                 -- IMAP mailbox path, or Gmail label name
    kind TEXT NOT NULL DEFAULT 'folder', -- 'folder' | 'gmail_label'
    special_use TEXT,                    -- '\Inbox', '\Sent', '\Drafts', '\Trash', '\Junk', '\All', ...
    is_sync_source INTEGER NOT NULL DEFAULT 1, -- 1 = UIDs are fetched from this mailbox (Gmail: All Mail, Spam, Trash only)
    uid_validity INTEGER DEFAULT 0,
    last_synced_uid INTEGER DEFAULT 0,
    highest_modseq INTEGER DEFAULT 0,   -- CONDSTORE: detect flag/label changes cheaply
    unread_count INTEGER DEFAULT 0,
    UNIQUE(account_id, path)
);

CREATE TABLE IF NOT EXISTS messages (
    seq INTEGER PRIMARY KEY,            -- stable rowid alias; FTS5 external content key (VACUUM-safe)
    id TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE, -- source mailbox the UID belongs to
    uid INTEGER NOT NULL,
    gm_msgid INTEGER,                   -- Gmail X-GM-MSGID (stable across labels/moves); NULL for non-Gmail
    gm_thrid INTEGER,                   -- Gmail X-GM-THRID (conversation id); NULL for non-Gmail
    message_id_header TEXT,
    subject TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    from_address TEXT NOT NULL DEFAULT '',
    to_json TEXT NOT NULL DEFAULT '[]',
    cc_json TEXT NOT NULL DEFAULT '[]',
    timestamp_unix INTEGER NOT NULL,
    is_read INTEGER NOT NULL DEFAULT 0,
    is_flagged INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    snippet TEXT NOT NULL DEFAULT '',
    text_body TEXT NOT NULL DEFAULT '',
    html_body TEXT NOT NULL DEFAULT '',
    raw_blob_path TEXT,
    UNIQUE(folder_id, uid),
    UNIQUE(account_id, gm_msgid)
);

-- Which folders/labels a message appears in. Non-Gmail: exactly one row (its folder).
-- Gmail: one row per label (from X-GM-LABELS), so each email is stored once but shows in every label view.
CREATE TABLE IF NOT EXISTS message_folders (
    message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    timestamp_unix INTEGER NOT NULL,    -- denormalized from messages for fast paginated listing
    PRIMARY KEY (message_seq, folder_id)
);

CREATE INDEX IF NOT EXISTS idx_message_folders_list ON message_folders(folder_id, timestamp_unix DESC);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(account_id, gm_thrid);

-- Full-Text Search: external-content FTS5 index over messages (bodies stored once).
-- unicode61 + remove_diacritics 2: word matching in any language, accent-insensitive ("cafe" finds "café").
-- prefix '2 3': fast search-as-you-type prefix queries ("fak*" finds "faktura").
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    subject,
    from_name,
    from_address,
    text_body,
    content='messages',
    content_rowid='seq',
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3'
);

CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
    VALUES (new.seq, new.subject, new.from_name, new.from_address, new.text_body);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
    VALUES ('delete', old.seq, old.subject, old.from_name, old.from_address, old.text_body);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_au
AFTER UPDATE OF subject, from_name, from_address, text_body ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
    VALUES ('delete', old.seq, old.subject, old.from_name, old.from_address, old.text_body);
    INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
    VALUES (new.seq, new.subject, new.from_name, new.from_address, new.text_body);
END;
```

#### Search rules
- **HTML-only mail must be searchable:** when a message has no `text/plain` part, the sync worker converts `html_body` to plain text (strip tags, scripts and styles, decode entities, collapse whitespace) and stores it in `text_body`. `text_body` must never be empty when the message has any body.
- **Ranking:** order results by relevance with subject weighted highest, e.g. `ORDER BY bm25(messages_fts, 10.0, 4.0, 4.0, 1.0)` (subject, from_name, from_address, text_body), with `timestamp_unix DESC` as a tie-breaker.
- **Query handling:** treat user input as plain words, not raw FTS5 syntax. Quote each term and add `*` to the last one for search-as-you-type (`invoice march` → `"invoice" "march"*`), so stray characters like `-`, `:` or `"` never cause syntax errors.
- **Join back** via `messages.seq = messages_fts.rowid` to return `messages.id`. Use `snippet()`/`highlight()` for match previews in results.
- **Recovery:** `INSERT INTO messages_fts(messages_fts) VALUES('rebuild');` rebuilds the index from `messages` if it is ever inconsistent.

---


## 6. Critical Implementation Rules & Security Constraints
### A. HTML Email Sandboxing (frontend/qml/MailViewer.qml & MailUrlInterceptor.cpp)
 * JavaScript & Storage Disabled: In WebEngineView, explicitly set:
   * settings.javascriptEnabled: false
   * settings.localStorageEnabled: false
   * settings.pluginsEnabled: false
   * settings.autoLoadImages: true (controlled via interceptor so local cid: images work while remote URLs are blocked).
 * Remote Tracker Blocking: Subclass QWebEngineUrlRequestInterceptor. Intercept any request whose scheme is http or https (excluding data: or cid: schemes). Unless the user has explicitly clicked "Load Remote Images" for the active message_id, block the request (info.block(true)) and emit a Qt signal remoteImageBlocked() so QML displays an unobtrusive warning bar.
 * External Links: Intercept navigation requests (NavigationTypeLinkClicked) in onNavigationRequested, set request.action = WebEngineView.IgnoreRequest, and open the URL in the user's default browser via QDesktopServices::openUrl().
### B. Hyprland & Wayland Integration (frontend/src/main.cpp & Main.qml)
 * Force Wayland platform plugin if unset: qputenv("QT_QPA_PLATFORM", "wayland"); before constructing QGuiApplication.
 * Set QGuiApplication::setDesktopFileName("mailand-ui"); so Hyprland window rules (windowrulev2 = float, class:^(mailand-ui)$ or tiling rules) match reliably.
 * Use ApplicationWindow { flags: Qt.FramelessWindowHint | Qt.Window } so Hyprland manages borders, gaps, and focus rings cleanly without duplicate CSD titlebars.
 * Implement global keyboard navigation in QML:
   * j / k: Next / previous message in MessageListPane
   * Enter / l: Focus reader pane
   * h / Escape: Return focus to message list / close modal
   * /: Focus search bar (SearchMessages RPC)
   * c: Open ComposerModal
   * r: Reply to active message
   * m: Toggle read/unread (SetMessageFlags RPC)
   * s: Toggle flag/star (SetMessageFlags RPC)
### C. Go Backend Concurrency & Storage (backend/)
 * Store passwords/OAuth refresh tokens exclusively via zalando/go-keyring (never plaintext in config.toml).
 * Run one IMAP sync goroutine per configured account using go-imap/v2 with IMAP IDLE support on INBOX for instant push notifications, plus periodic polling (every 5 minutes) for secondary folders.
 * On new message arrival or flag mutation, persist to SQLite first (messages_fts is updated automatically by triggers), and broadcast a DaemonEvent to all active SubscribeEvents gRPC streams.
 * Socket lifecycle: Bind to $XDG_RUNTIME_DIR/mailand.sock (fallback /tmp/mailand-$UID.sock) with 0600 permissions; remove any stale socket file on startup before net.Listen("unix", sockPath).

### D. Gmail Labels: One Copy per Email
Gmail exposes each label as an IMAP folder, so the same email appears in INBOX, every label folder, and `[Gmail]/All Mail`. Store each email **once** and model labels as membership.

 * **Sync sources:** for Gmail accounts, only fetch UIDs from `[Gmail]/All Mail`, `[Gmail]/Spam` and `[Gmail]/Trash` (detect via SPECIAL-USE `\All`, `\Junk`, `\Trash`, not by name, since names are localized). These have `is_sync_source = 1`. Label folders from `LIST` are stored with `kind = 'gmail_label'`, `is_sync_source = 0` and are never fetched directly.
 * **Fetch** `X-GM-MSGID`, `X-GM-THRID` and `X-GM-LABELS` with each message. Upsert by `(account_id, gm_msgid)`: a message moved to Trash/Spam keeps its `gm_msgid` and gets a new source `folder_id`/`uid`, so update the existing row instead of inserting a duplicate.
 * **Labels → `message_folders`:** map each X-GM-LABELS entry to a folder row (`\Inbox` → INBOX, `\Sent` → Sent, `\Important`, `\Starred`, user labels). `\Starred` also sets `is_flagged`. Replace a message's rows whenever its labels change.
 * **Change detection:** use CONDSTORE (`CHANGEDSINCE highest_modseq`) on All Mail to pick up label and flag changes without refetching everything.
 * **Listing & search:** `ListMessages(folder_id)` reads from `message_folders` joined to `messages`. Search hits `messages` once per email, so there are no duplicates.
 * **Non-Gmail accounts** use the same tables: every folder is a sync source and each message has exactly one `message_folders` row.
 * **Implementation status (Milestone 2):** go-imap v2 beta.9 cannot fetch `X-GM-MSGID`/`X-GM-THRID`/`X-GM-LABELS`, so the fallback in `backend/internal/mail/gmail.go` is used: messages are keyed by (All Mail folder, uid) — still one copy per email — and label membership is built by fetching UID + ENVELOPE (Message-ID only) per label folder and matching by Message-ID. `gm_msgid`/`gm_thrid` stay NULL until go-imap gains X-GM support. Requires "Show in IMAP" enabled for All Mail in Gmail settings.

### E. Gmail via OAuth2 (required from Milestone 2)
Gmail is the primary target account and must work with OAuth2 from the start. Microsoft/Outlook OAuth is out of scope. Password auth remains available for other providers.

 * **Endpoints:**
   * IMAP: `imap.gmail.com:993` (implicit TLS)
   * SMTP: `smtp.gmail.com:465` (implicit TLS) or `:587` (STARTTLS)
   * CalDAV: `https://apidata.googleusercontent.com/caldav/v2/` (Milestone 5)
   * CardDAV: `https://www.googleapis.com/carddav/v1/principals/<email>/lists/default/` (Milestone 5)
 * **Scopes:** `https://mail.google.com/` (IMAP + SMTP). Calendar and contacts scopes (`https://www.googleapis.com/auth/calendar`, `https://www.googleapis.com/auth/carddav`) are requested in the same consent so the user only logs in once.
 * **OAuth client:** the user creates their own Google Cloud project with an OAuth client of type **Desktop app**. `client_id` and `client_secret` go in `config.toml` (Google does not treat desktop client secrets as confidential). Document this setup in the README.
   * **Gotcha:** while the consent screen is in **Testing** status, Google expires refresh tokens after 7 days. Set the publishing status to **In production** (personal use, unverified is fine; the user clicks past the "unverified app" warning once).
 * **Login flow (installed-app, loopback + PKCE):** `mailand-daemon login <account_id>` (CLI subcommand; later also a UI button via a `StartOAuthLogin` RPC):
   1. Start a temporary HTTP listener on `127.0.0.1:<random port>`.
   2. Open the Google consent URL (with PKCE `code_challenge`, `access_type=offline`, `prompt=consent`) in the default browser via `xdg-open`.
   3. Receive the `code` on the loopback redirect, exchange it for tokens, store the **refresh token** in the keyring under `mailand/<account_id>`, then shut the listener down.
 * **Token use:** build an `oauth2.TokenSource` from the stored refresh token (wrapped in `oauth2.ReuseTokenSource`) and use it everywhere:
   * IMAP: `imapclient.Client.Authenticate()` with a SASL `OAUTHBEARER` or `XOAUTH2` client using a fresh access token.
   * SMTP: `go-smtp` `Client.Auth()` with the same SASL mechanism.
   * CalDAV/CardDAV: pass `oauth2.NewClient(ctx, tokenSource)` as the HTTP client to `go-webdav`.
 * **Reconnects:** access tokens last ~1 hour. Long-lived IDLE connections must reconnect and re-authenticate with a fresh token when the server drops them. On `invalid_grant` (revoked or expired refresh token), stop syncing that account and emit a `DaemonEvent` of type `ERROR` with `detail = "reauth_required"` so the UI can prompt a new login.

---

## 7. Phased Execution Plan for the Agent
Execute in strict milestone order. Verify each milestone compiles and passes basic integration tests before moving to the next:
 * Milestone 1: Scaffolding & IPC Pipeline
   * Create directory structure, Makefile, proto/mailand.proto, and code generation commands (protoc for Go and C++).
   * Implement minimal mailand-daemon listening on $XDG_RUNTIME_DIR/mailand.sock serving mock ListAccounts and ListMessages responses.
   * Implement minimal mailand-ui (CMakeLists.txt, main.cpp, RpcClient.cpp) that connects to the Unix socket and prints/displays the mock accounts.
 * Milestone 2: SQLite Storage & IMAP Sync Engine
   * Implement ~/.config/mailand/config.toml parser and go-keyring integration.
   * Implement Gmail OAuth2 (section 6E): `mailand-daemon login` loopback+PKCE flow, refresh-token storage, OAUTHBEARER/XOAUTH2 for IMAP and SMTP. Verify against a real Gmail account before moving on.
   * Implement SQLite schema initialization, FTS5 triggers, and paginated queries in backend/internal/store.
   * Implement go-imap/v2 sync worker (fetch folders, incremental header/body sync via UIDs, MIME parsing with go-message into text_body and html_body, HTML-to-text fallback for text_body so HTML-only mail is searchable, Gmail single-copy label sync per section 6D, and IMAP IDLE).
 * Milestone 3: Core Qt 6 / QML Three-Pane Mail UI
   * Build FolderModel and MessageModel (QAbstractListModel subclasses backed by RpcClient).
   * Build Main.qml, SidebarPane.qml, MessageListPane.qml, ReaderPane.qml, and locked-down MailViewer.qml with MailUrlInterceptor.
   * Wire Vim keybindings, live search (SearchMessages), flag toggling (SetMessageFlags), and real-time refresh via SubscribeEvents.
 * Milestone 4: SMTP Composer & Systemd Integration
   * Implement SendMessage in Go using emersion/go-smtp and append sent messages to the Sent folder via IMAP APPEND.
   * Build ComposerModal.qml with reply quoting (In-Reply-To / References headers).
   * Add deploy/mailand-daemon.service and verify clean startup/shutdown under Hyprland.
   * Add deploy/mailand-ui.desktop and deploy/PKGBUILD (builds backend with `go build` and frontend with CMake; installs `/usr/bin/mailand-daemon`, `/usr/bin/mailand-ui`, `/usr/lib/systemd/user/mailand-daemon.service`, `/usr/share/applications/mailand-ui.desktop`). Verify with `makepkg -si` that the app shows up in the launcher.
 * Milestone 5: CalDAV & CardDAV Integration
   * Extend mailand.proto and SQLite schema with calendars, events, addressbooks, and contacts.
   * Implement CalDAV/CardDAV sync in backend/internal/dav using emersion/go-webdav.
   * Add Calendar and Contacts views to the QML sidebar switcher.
   * **Implementation status (0.2.0):** read-only sync. Discovery: principal → home set → collections, falling back to the configured URL as one collection. Change detection is a Depth-1 PROPFIND for ETags (webdav.Client.ReadDir is not used: it fails when a server omits getcontentlength) plus multiget of new/changed objects. Objects are stored raw (schema v2) with an instance time range; recurring events are expanded per query in the daemon (go-ical + rrule-go; RECURRENCE-ID overrides, EXDATE, cancelled occurrences; Windows/unknown TZIDs mapped or read as floating). Contacts feed composer autocomplete. Event creation/editing is not implemented yet.
