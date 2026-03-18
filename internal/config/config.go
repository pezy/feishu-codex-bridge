package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultHTTPAddr           = "127.0.0.1:8787"
	defaultRecentContextLimit = 12
	defaultReadTimeout        = 15 * time.Second
	defaultWriteTimeout       = 15 * time.Second
	defaultShutdownTimeout    = 10 * time.Second
	defaultCodexTimeout       = 20 * time.Minute
)

type Config struct {
	AppID              string        `yaml:"app_id"`
	AppSecret          string        `yaml:"app_secret"`
	AuthorizedOpenID   string        `yaml:"authorized_open_id"`
	BotOpenID          string        `yaml:"bot_open_id"`   // 机器人的 OpenID，用于群聊 @ 触发
	BotMentionNames    []string      `yaml:"bot_mention_names"` // 机器人被 @ 时的名称列表
	AuthorizedGroupIDs []string      `yaml:"authorized_group_chat_ids"`
	HTTPAddr           string        `yaml:"http_addr"`
	DefaultWorkDir     string        `yaml:"default_work_dir"`
	CodexPath          string        `yaml:"codex_path"`
	CodexTimeout       time.Duration `yaml:"-"`
	CodexTimeoutRaw    string        `yaml:"codex_timeout"`
	LogLevel           string        `yaml:"log_level"`
	AckReactionType    string        `yaml:"ack_reaction_type"`
	RecentContextLimit int           `yaml:"recent_context_limit"`
	ReplyRetryCount    int           `yaml:"reply_retry_count"`
	ReadTimeout        time.Duration `yaml:"-"`
	ReadTimeoutRaw     string        `yaml:"read_timeout"`
	WriteTimeout       time.Duration `yaml:"-"`
	WriteTimeoutRaw    string        `yaml:"write_timeout"`
	ShutdownTimeout    time.Duration `yaml:"-"`
	ShutdownTimeoutRaw string        `yaml:"shutdown_timeout"`
	AppSupportDir      string        `yaml:"app_support_dir"`
	LogDir             string        `yaml:"log_dir"`
	DBPath             string        `yaml:"db_path"`
}

func Load(path string) (Config, error) {
	appSupportDir, err := resolveAppSupportDir()
	if err != nil {
		return Config{}, err
	}
	defaultWorkDir := resolveDefaultWorkDir()
	defaultCodexPath := resolveDefaultCodexPath()

	cfg := Config{
		HTTPAddr:           defaultHTTPAddr,
		DefaultWorkDir:     defaultWorkDir,
		CodexPath:          defaultCodexPath,
		CodexTimeout:       defaultCodexTimeout,
		LogLevel:           "info",
		AckReactionType:    "Typing",
		RecentContextLimit: defaultRecentContextLimit,
		ReplyRetryCount:    3,
		ReadTimeout:        defaultReadTimeout,
		WriteTimeout:       defaultWriteTimeout,
		ShutdownTimeout:    defaultShutdownTimeout,
		AppSupportDir:      appSupportDir,
		LogDir:             defaultLogDir(),
		DBPath:             filepath.Join(appSupportDir, "bridge.db"),
	}

	configPath := path
	if configPath == "" {
		configPath = defaultConfigPath(appSupportDir)
	}

	if data, readErr := os.ReadFile(configPath); readErr == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", configPath, readErr)
	}

	applyEnvOverrides(&cfg)
	cfg.normalize(configPath)

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func resolveDefaultWorkDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Service")
}

func resolveDefaultCodexPath() string {
	path, err := exec.LookPath("codex")
	if err == nil {
		return path
	}
	return "codex"
}

func resolveAppSupportDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "feishu-codex-bridge"), nil
}

func defaultLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./logs"
	}
	return filepath.Join(home, "Library", "Logs", "feishu-codex-bridge")
}

func defaultConfigPath(appSupportDir string) string {
	if envPath := strings.TrimSpace(os.Getenv("FEISHU_CODEX_BRIDGE_CONFIG")); envPath != "" {
		return envPath
	}
	return filepath.Join(appSupportDir, "config.yaml")
}

func applyEnvOverrides(cfg *Config) {
	overrideString(&cfg.AppID, "FEISHU_CODEX_BRIDGE_APP_ID")
	overrideString(&cfg.AppSecret, "FEISHU_CODEX_BRIDGE_APP_SECRET")
	overrideString(&cfg.AuthorizedOpenID, "FEISHU_CODEX_BRIDGE_AUTHORIZED_OPEN_ID")
	overrideStringSlice(&cfg.AuthorizedGroupIDs, "FEISHU_CODEX_BRIDGE_AUTHORIZED_GROUP_CHAT_IDS")
	overrideString(&cfg.HTTPAddr, "FEISHU_CODEX_BRIDGE_HTTP_ADDR")
	overrideString(&cfg.DefaultWorkDir, "FEISHU_CODEX_BRIDGE_DEFAULT_WORK_DIR")
	overrideString(&cfg.CodexPath, "FEISHU_CODEX_BRIDGE_CODEX_PATH")
	overrideString(&cfg.CodexTimeoutRaw, "FEISHU_CODEX_BRIDGE_CODEX_TIMEOUT")
	overrideString(&cfg.LogLevel, "FEISHU_CODEX_BRIDGE_LOG_LEVEL")
	overrideString(&cfg.AckReactionType, "FEISHU_CODEX_BRIDGE_ACK_REACTION_TYPE")
	overrideInt(&cfg.RecentContextLimit, "FEISHU_CODEX_BRIDGE_RECENT_CONTEXT_LIMIT")
	overrideInt(&cfg.ReplyRetryCount, "FEISHU_CODEX_BRIDGE_REPLY_RETRY_COUNT")
	overrideString(&cfg.ReadTimeoutRaw, "FEISHU_CODEX_BRIDGE_READ_TIMEOUT")
	overrideString(&cfg.WriteTimeoutRaw, "FEISHU_CODEX_BRIDGE_WRITE_TIMEOUT")
	overrideString(&cfg.ShutdownTimeoutRaw, "FEISHU_CODEX_BRIDGE_SHUTDOWN_TIMEOUT")
	overrideString(&cfg.AppSupportDir, "FEISHU_CODEX_BRIDGE_APP_SUPPORT_DIR")
	overrideString(&cfg.LogDir, "FEISHU_CODEX_BRIDGE_LOG_DIR")
	overrideString(&cfg.DBPath, "FEISHU_CODEX_BRIDGE_DB_PATH")
}

func overrideString(dst *string, envKey string) {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		*dst = value
	}
}

func overrideInt(dst *int, envKey string) {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			*dst = parsed
		}
	}
}

func overrideStringSlice(dst *[]string, envKey string) {
	value := strings.TrimSpace(os.Getenv(envKey))
	if value == "" {
		return
	}
	parts := strings.Split(value, ",")
	*dst = normalizeStringSlice(parts)
}

func (c *Config) normalize(configPath string) {
	c.AppID = strings.TrimSpace(c.AppID)
	c.AppSecret = strings.TrimSpace(c.AppSecret)
	c.AuthorizedOpenID = strings.TrimSpace(c.AuthorizedOpenID)
	c.AuthorizedGroupIDs = normalizeStringSlice(c.AuthorizedGroupIDs)
	c.HTTPAddr = strings.TrimSpace(c.HTTPAddr)
	c.DefaultWorkDir = strings.TrimSpace(c.DefaultWorkDir)
	c.CodexPath = strings.TrimSpace(c.CodexPath)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	c.AckReactionType = strings.TrimSpace(c.AckReactionType)

	if c.HTTPAddr == "" {
		c.HTTPAddr = defaultHTTPAddr
	}
	if c.DefaultWorkDir == "" {
		c.DefaultWorkDir = resolveDefaultWorkDir()
	}
	if c.CodexPath == "" {
		c.CodexPath = resolveDefaultCodexPath()
	}
	if c.RecentContextLimit <= 0 {
		c.RecentContextLimit = defaultRecentContextLimit
	}
	if c.AckReactionType == "" {
		c.AckReactionType = "Typing"
	}
	if c.ReplyRetryCount <= 0 {
		c.ReplyRetryCount = 3
	}
	if c.AppSupportDir == "" {
		c.AppSupportDir = filepath.Dir(configPath)
	}
	if c.LogDir == "" {
		c.LogDir = defaultLogDir()
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.AppSupportDir, "bridge.db")
	}

	c.CodexTimeout = parseDuration(c.CodexTimeoutRaw, defaultCodexTimeout)
	c.ReadTimeout = parseDuration(c.ReadTimeoutRaw, defaultReadTimeout)
	c.WriteTimeout = parseDuration(c.WriteTimeoutRaw, defaultWriteTimeout)
	c.ShutdownTimeout = parseDuration(c.ShutdownTimeoutRaw, defaultShutdownTimeout)
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		output = append(output, value)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func (c Config) validate() error {
	switch {
	case c.AppID == "":
		return errors.New("missing app_id or FEISHU_CODEX_BRIDGE_APP_ID")
	case c.AppSecret == "":
		return errors.New("missing app_secret or FEISHU_CODEX_BRIDGE_APP_SECRET")
	case c.AuthorizedOpenID == "":
		return errors.New("missing authorized_open_id or FEISHU_CODEX_BRIDGE_AUTHORIZED_OPEN_ID")
	}

	for _, dir := range []string{c.AppSupportDir, c.LogDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	return nil
}
