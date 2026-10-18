package server

import (
	"context"
	"log/slog"

	"github.com/user/dbhub/internal/assetapi"
	"github.com/user/dbhub/internal/automation"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
)

func NewAutomation(cfg *config.Config, log *slog.Logger, deps *Deps) *automation.Service {
	if deps == nil || deps.UserRepo == nil || deps.Conns == nil || deps.Meta == nil || deps.AuditJournal == nil {
		return nil
	}
	drivers := connectorruntime.NewConfigured(cfg.JDBCURL, cfg.JDBCToken)
	return &automation.Service{Repo: db.NewAutomationRepository(deps.UserRepo), Users: deps.UserRepo, Conns: deps.Conns, Tunnels: deps.Tunnels, Assets: assetapi.NewHandler(deps.Conns, deps.Tunnels, deps.Meta, deps.UserRepo, deps.History, log, drivers), Drivers: drivers, Journal: deps.AuditJournal, Config: cfg.Operations, Log: log}
}
func StartAutomation(ctx context.Context, cfg *config.Config, log *slog.Logger, deps *Deps) *automation.Service {
	service := NewAutomation(cfg, log, deps)
	if service != nil {
		service.Start(ctx)
	}
	return service
}
