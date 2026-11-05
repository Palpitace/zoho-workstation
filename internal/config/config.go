// input: 根目录 .env 配置，不读取或覆盖进程环境。
// output: 持续发现起始日期； 经校验的服务、监听 IP 及有界同步批次/并发配置，错误不含值。
// pos: 配置加载边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Zoho struct {
	BaseURL, AccountsURL, ClientID, ClientSecret, RefreshToken, OrgID, DepartmentID string
}

type Model struct {
	Enabled        bool
	URL, Key, Name string
}

const DefaultSyncStartTime = "2026-11-05T00:00:00+08:00"

type Config struct {
	SyncStartTime                                         time.Time
	Environment, ListenHost, Port, LogLevel, DatabasePath string
	Zoho                                                  Zoho
	Jev, Bub                                              Model
	SyncInterval, SyncLookback                            time.Duration
	SyncBatchSize, SyncConcurrency                        int
}

// Load reads only the specified file so subprocesses never need inherited secrets.
func Load(path string) (Config, error) {
	v, err := godotenv.Read(path)
	if err != nil {
		return Config{}, errors.New("cannot read .env; check file and syntax")
	}
	var c Config
	value := func(key, fallback string) string {
		if s := v[key]; s != "" {
			return s
		}
		return fallback
	}
	c.SyncStartTime, err = time.Parse(time.RFC3339, value("SYNC_START_TIME", DefaultSyncStartTime))
	if err != nil {
		return c, errors.New("invalid SYNC_START_TIME: RFC3339 timestamp required")
	}
	c.Environment = value("APP_ENV", "development")
	c.ListenHost = value("APP_LISTEN_HOST", "127.0.0.1")
	if net.ParseIP(c.ListenHost) == nil {
		return c, errors.New("invalid APP_LISTEN_HOST: IP address required")
	}
	c.Port = value("APP_PORT", "8080")
	c.LogLevel = value("APP_LOG_LEVEL", "info")
	c.DatabasePath = value("DATABASE_PATH", "./data/app.db")
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return c, errors.New("invalid APP_PORT")
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return c, errors.New("invalid APP_LOG_LEVEL")
	}
	for key, dst := range map[string]*time.Duration{"SYNC_INTERVAL": &c.SyncInterval, "SYNC_LOOKBACK": &c.SyncLookback} {
		fallback := "2m"
		if key == "SYNC_LOOKBACK" {
			fallback = "24h"
		}
		d, e := time.ParseDuration(value(key, fallback))
		if e != nil || d <= 0 {
			return c, fmt.Errorf("invalid %s", key)
		}
		*dst = d
	}
	for _, setting := range []struct {
		key, fallback string
		max           int
		dst           *int
	}{
		{"SYNC_BATCH_SIZE", "9", 50, &c.SyncBatchSize},
		{"SYNC_CONCURRENCY", "3", 5, &c.SyncConcurrency},
	} {
		n, err := strconv.Atoi(value(setting.key, setting.fallback))
		if err != nil || n < 1 || n > setting.max {
			return c, fmt.Errorf("invalid %s: expected 1..%d", setting.key, setting.max)
		}
		*setting.dst = n
	}
	for _, key := range []string{"ENABLE_JEV", "ENABLE_BUB", "ENABLE_WECOM"} {
		if v[key] != "true" && v[key] != "false" {
			return c, fmt.Errorf("invalid %s: use true or false", key)
		}
	}
	if v["ENABLE_WECOM"] != "false" {
		return c, errors.New("ENABLE_WECOM must remain false for this release")
	}
	c.Zoho = Zoho{v["ZOHO_BASE_URL"], v["ZOHO_ACCOUNTS_URL"], v["ZOHO_CLIENT_ID"], v["ZOHO_CLIENT_SECRET"], v["ZOHO_REFRESH_TOKEN"], v["ZOHO_ORG_ID"], v["ZOHO_DEPARTMENT_ID"]}
	c.Jev = Model{v["ENABLE_JEV"] == "true", v["JEV_API_URL"], v["JEV_API_KEY"], v["JEV_MODEL"]}
	c.Bub = Model{v["ENABLE_BUB"] == "true", v["BUB_API_BASE"], v["BUB_API_KEY"], v["BUB_MODEL"]}
	required := []string{"ZOHO_BASE_URL", "ZOHO_ACCOUNTS_URL", "ZOHO_CLIENT_ID", "ZOHO_CLIENT_SECRET", "ZOHO_REFRESH_TOKEN", "ZOHO_ORG_ID", "ZOHO_DEPARTMENT_ID"}
	for _, model := range []struct {
		prefix  string
		enabled bool
	}{{"JEV", c.Jev.Enabled}, {"BUB", c.Bub.Enabled}} {
		if model.enabled {
			required = append(required, model.prefix+"_API_KEY", model.prefix+"_MODEL")
		}
	}
	for _, key := range required {
		if strings.TrimSpace(v[key]) == "" {
			return c, fmt.Errorf("missing %s", key)
		}
	}
	for key, enabled := range map[string]bool{"ZOHO_BASE_URL": true, "ZOHO_ACCOUNTS_URL": true, "JEV_API_URL": c.Jev.Enabled, "BUB_API_BASE": c.Bub.Enabled} {
		if !enabled {
			continue
		}
		u, e := url.Parse(v[key])
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("invalid %s: HTTPS URL required", key)
		}
		if strings.HasPrefix(key, "ZOHO_") && u.Path != "" && u.Path != "/" {
			return c, fmt.Errorf("invalid %s: origin only", key)
		}
	}
	return c, nil
}
