package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("aicrm stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := platformconfig.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := compose(ctx, cfg)
	if err != nil {
		return err
	}
	defer application.Close()
	if cfg.Role == platformconfig.RolePaymentReconcile {
		if application.paymentReconciliation == nil {
			return errors.New("payment reconciliation is not composed")
		}
		payment, reconcileErr := application.paymentReconciliation.ReconcileWeChatPayPayment(ctx, cfg.PaymentReconcileID)
		if reconcileErr != nil {
			return reconcileErr
		}
		// Keep this operational proof deliberately narrow: no merchant order,
		// provider transaction, customer or secret enters the log. The service
		// itself has performed the signed Provider read and its same-UoW fanout.
		slog.Info("payment reconciliation complete", "payment_id", payment.ID, "status", payment.Status, "provider_query_performed", true, "release_sha", cfg.ReleaseSHA)
		return nil
	}
	if cfg.Role == platformconfig.RoleWorker {
		processed := 0
		var processErr error
		if cfg.WeCom.CallbackEnabled {
			processed, processErr = application.weComProcessor.ProcessOnce(ctx, cfg.WorkerOwner, cfg.WorkerLimit)
		}
		if processErr == nil && cfg.WeCom.MessageArchiveEnabled {
			archiveProcessed, archiveErr := application.weComArchiveProcessor.ProcessOnce(ctx, cfg.WorkerOwner, cfg.WorkerLimit)
			processed += archiveProcessed
			processErr = archiveErr
		}
		if processErr == nil && application.customerSync.Ready() {
			if cfg.CustomerSyncTrigger != "" {
				location, _ := time.LoadLocation("Asia/Shanghai")
				key := "initial:customer-directory-v1"
				if cfg.CustomerSyncTrigger == "daily" {
					key = "daily:" + time.Now().In(location).Format("2006-01-02")
				}
				_, _, processErr = application.customerSync.CreateScheduled(ctx, cfg.CustomerSyncTrigger, key)
			}
		}
		if processErr == nil && application.hxcDashboard.Ready() && cfg.HXCDashboard.SyncTrigger != "" {
			location, _ := time.LoadLocation("Asia/Shanghai")
			mode := "inspect"
			if cfg.HXCDashboard.IdentityWriteEnabled {
				mode = "apply"
			}
			key := "initial:hxc-dashboard-v2:" + mode
			if cfg.ReleaseSHA != "" && cfg.ReleaseSHA != "dev" {
				key += ":" + cfg.ReleaseSHA
			}
			if cfg.HXCDashboard.SyncTrigger == "scheduled" {
				key = "scheduled:" + time.Now().In(location).Format("2006-01-02T15") + ":hxc-dashboard-v2:" + mode
			}
			_, _, processErr = application.hxcDashboard.Create(ctx, key, cfg.HXCDashboard.SyncTrigger, 0)
		}
		if processErr == nil {
			slog.Info("worker oneshot complete", "callback_processed", processed, "customer_sync_trigger", cfg.CustomerSyncTrigger != "", "hxc_sync_trigger", cfg.HXCDashboard.SyncTrigger != "", "release_sha", cfg.ReleaseSHA)
		}
		return processErr
	}
	if cfg.Role == platformconfig.RoleEffectsWorker {
		return application.effectsRuntime.Run(ctx)
	}
	if err = application.bootstrap(ctx, cfg.Bootstrap); err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           application.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("aicrm started", "address", cfg.ListenAddress, "role", cfg.Role, "release_sha", cfg.ReleaseSHA)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err = <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
