// Command mailand-daemon is the headless mailand backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // fallback zone database for calendar TZIDs

	"mailand/backend/internal/config"
	"mailand/backend/internal/dav"
	"mailand/backend/internal/mail"
	"mailand/backend/internal/rpc"
	"mailand/backend/internal/rpc/pb"
	"mailand/backend/internal/send"
	"mailand/backend/internal/store"
)

const shutdownTimeout = 5 * time.Second

// version is set at build time from the repo's VERSION file
// (-ldflags "-X main.version=..."); "dev" for plain `go build`.
var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("mailand-daemon", version)
		return
	}
	if handled, code := runSubcommand(os.Args[1:]); handled {
		os.Exit(code)
	}
	socket := flag.String("socket", config.SocketPath(), "Unix socket path to listen on")
	offline := flag.Bool("offline", false, "dev mode: no IMAP; serve accounts from the store, flag changes write the store only")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(log, *socket, *offline); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, socket string, offline bool) error {
	cfg, err := config.Load(config.ConfigPath())
	switch {
	case offline && err != nil:
		cfg = &config.Config{} // config is optional offline; accounts come from the store
	case errors.Is(err, fs.ErrNotExist):
		log.Warn("no config file; starting with zero accounts", "path", config.ConfigPath())
		cfg = &config.Config{}
	case err != nil:
		return err
	}

	// Bind first so a second daemon fails before touching the database.
	lis, err := rpc.ListenUnix(socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)

	st, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	broker := rpc.NewBroker()
	defer broker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// A nil *mail.Manager must not be stored in the interface: rpc checks
	// syncer == nil to report FailedPrecondition when there are no accounts.
	var syncer rpc.Syncer
	var sender rpc.Sender
	var mgr *mail.Manager
	var davMgr *dav.Manager
	if offline {
		syncer = rpc.NewOfflineSyncer(st, broker)
		sender = send.NewOfflineSender(st, config.BlobDir(), func(accountID, folderID string) {
			broker.Publish(&pb.DaemonEvent{Type: pb.DaemonEvent_NEW_MESSAGES, AccountId: accountID, FolderId: folderID, Detail: "1"})
		})
		log.Warn("offline mode: no IMAP, flag changes are local only")
	} else if len(cfg.Accounts) > 0 {
		mgr = mail.NewManager(cfg, st, config.BlobDir(), func(ev mail.Event) {
			broker.Publish(toPB(ev)) // non-blocking
		}, log)
		if err := mgr.Start(ctx); err != nil {
			return err
		}
		davMgr = dav.NewManager(cfg, st, func(ev dav.Event) { broker.Publish(davToPB(ev)) }, log)
		davMgr.Start(ctx)
		syncer = syncers{mgr, davMgr}
		sender = send.NewSender(ctx, cfg, st, mgr.TriggerSync, log)
	}

	srv := rpc.NewServer(st, syncer, broker, log)
	srv.SetSender(sender)
	g := srv.Register()

	errc := make(chan error, 1)
	go func() { errc <- g.Serve(lis) }()
	log.Info("listening", "version", version, "socket", socket, "accounts", len(cfg.Accounts))

	select {
	case err = <-errc:
	case <-ctx.Done():
	}

	log.Info("shutting down")
	srv.Close() // end event streams so GracefulStop can finish
	done := make(chan struct{})
	go func() { g.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		log.Warn("graceful stop timed out, forcing")
		g.Stop()
	}
	if mgr != nil {
		mgr.Stop() // before st.Close (deferred) so workers never write to a closed DB
	}
	if davMgr != nil {
		davMgr.Stop()
	}
	return err
}

func toPB(ev mail.Event) *pb.DaemonEvent {
	t := pb.DaemonEvent_ERROR
	switch ev.Type {
	case mail.EventSyncStarted:
		t = pb.DaemonEvent_SYNC_STARTED
	case mail.EventSyncFinished:
		t = pb.DaemonEvent_SYNC_FINISHED
	case mail.EventNewMessages:
		t = pb.DaemonEvent_NEW_MESSAGES
	case mail.EventFlagsUpdated:
		t = pb.DaemonEvent_FLAGS_UPDATED
	case mail.EventSyncProgress:
		t = pb.DaemonEvent_SYNC_PROGRESS
	}
	return &pb.DaemonEvent{Type: t, AccountId: ev.AccountID, FolderId: ev.FolderID, Detail: ev.Detail}
}

// syncers makes TriggerSync (Send/Receive, Ctrl+R) refresh mail, calendars
// and contacts; flag changes go to mail only.
type syncers struct {
	*mail.Manager
	dav *dav.Manager
}

func (s syncers) TriggerSync(accountID string) error {
	if err := s.Manager.TriggerSync(accountID); err != nil {
		return err
	}
	s.dav.TriggerSync(accountID)
	return nil
}

func davToPB(ev dav.Event) *pb.DaemonEvent {
	t := pb.DaemonEvent_ERROR
	switch ev.Kind {
	case dav.CalendarsUpdated:
		t = pb.DaemonEvent_CALENDARS_UPDATED
	case dav.ContactsUpdated:
		t = pb.DaemonEvent_CONTACTS_UPDATED
	}
	return &pb.DaemonEvent{Type: t, AccountId: ev.AccountID, Detail: ev.Detail}
}
