package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/proxy"
	"gopkg.in/telebot.v4"
	botengine "mars-rescue-bot/internal/bot"
	"mars-rescue-bot/internal/config"
	"mars-rescue-bot/internal/database"
	"mars-rescue-bot/internal/handler"
	"mars-rescue-bot/internal/logger"
	"mars-rescue-bot/internal/maintenance"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
)

func main() {
	logger.Init()

	config.Init()

	if config.BotToken == "" {
		slog.Error("BOT_TOKEN is not set")
		os.Exit(1)
	}

	if err := database.Init(); err != nil {
		slog.Error("init database failed", "error", err)
		os.Exit(1)
	}

	client := newHTTPClient()

	pref := telebot.Settings{
		Token:  config.BotToken,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
		Client: client,
	}

	bot, err := telebot.NewBot(pref)
	if err != nil {
		slog.Error("create bot failed", "error", err)
		os.Exit(1)
	}

	slog.Info("已连接到 Telegram", "bot_id", bot.Me.ID)

	bot.Use(logger.TraceMiddleware)

	db := database.DB
	r := repo.New(db)
	resolver := names.New(bot)

	cmdHandler := handler.NewCommandHandler(r, handler.NewAdminChecker(bot), resolver, config.DedupSecret)
	cmdHandler.Register(bot)

	processor := botengine.NewProcessor(r, config.DedupSecret, resolver, config.StatsLocation())
	msgHandler := handler.NewMessageHandler(processor)
	msgHandler.Register(bot)

	slog.Info("数据保留策略已启用", "days", config.DataRetentionDays)
	maintCtx, stopMaintenance := context.WithCancel(context.Background())
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		maintenance.New(database.DB, config.DataRetentionDays).Start(maintCtx, 24*time.Hour)
	}()

	slog.Info("火星救援 Bot 启动成功")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("收到退出信号，正在停止", "signal", sig.String())
		bot.Stop()
	}()

	// Start 会阻塞，直到 Stop 被调用。
	bot.Start()
	signal.Stop(sigCh)
	stopMaintenance()
	<-maintenanceDone

	if sqlDB, err := database.DB.DB(); err != nil {
		slog.Error("获取数据库连接失败", "error", err)
	} else if err := sqlDB.Close(); err != nil {
		slog.Error("关闭数据库失败", "error", err)
	}

	slog.Info("Bot 已停止")
}

func newHTTPClient() *http.Client {
	if config.ProxyURL == "" {
		return nil
	}

	u, err := url.Parse(config.ProxyURL)
	if err != nil {
		slog.Warn("PROXY_URL 格式无效，忽略代理", "proxy_url", config.ProxyURL, "error", err)
		return nil
	}

	transport := &http.Transport{}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
		slog.Info("使用 HTTP 代理", "proxy_url", config.ProxyURL)

	case "socks5":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			slog.Error("SOCKS5 代理初始化失败", "proxy_url", config.ProxyURL, "error", err)
			return nil
		}
		transport.Dial = dialer.Dial
		slog.Info("使用 SOCKS5 代理", "proxy_url", config.ProxyURL)

	default:
		slog.Warn("不支持的代理协议", "scheme", u.Scheme, "proxy_url", config.ProxyURL)
		return nil
	}

	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}
