package cli

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/joestump/sigbrowse/internal/config"
	"github.com/joestump/sigbrowse/internal/ingest"
	"github.com/joestump/sigbrowse/internal/web"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local HTMX web UI",
		Long: "serve runs the server-rendered HTMX web UI. It binds to loopback by\n" +
			"default; the UI has no authentication, so only expose it on a non-loopback\n" +
			"address behind your own access control.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig()
			if err != nil {
				return err
			}
			if addr, _ := cmd.Flags().GetString("listen-addr"); addr != "" {
				cfg.ListenAddr = addr
			}

			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()

			// Signals cancel the context for graceful shutdown.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			if cfg.IngestOnStart {
				if err := ingestOnStart(ctx, cfg); err != nil {
					slog.Warn("ingest-on-start failed; serving existing data", "error", err)
				}
			}

			srv, err := web.NewServer(st, cfg, slog.Default())
			if err != nil {
				return err
			}
			return srv.Run(ctx, cfg.ListenAddr)
		},
	}
	cmd.Flags().String("listen-addr", "", "override listen address (default 127.0.0.1:8787)")
	return cmd
}

// ingestOnStart runs a best-effort ingest pass before serving, when configured
// and an archive is available.
func ingestOnStart(ctx context.Context, cfg *config.Config) error {
	if err := requireArchive(cfg); err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = ingest.Run(ctx, st, ingest.Options{ArchiveRoot: cfg.ArchiveRoot})
	return err
}
