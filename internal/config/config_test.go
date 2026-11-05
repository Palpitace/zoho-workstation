// input: 临时合成 .env 及容器/本机监听地址。
// output: 默认仅本机、显式容器监听和非法地址拒绝的证据。
// pos: 启动配置边界验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"zoho-workstation/internal/config"
)

func TestListenHostIsExplicitAndValidated(t *testing.T) {
	const base = "ZOHO_BASE_URL=https://desk.example.invalid\nZOHO_ACCOUNTS_URL=https://accounts.example.invalid\nZOHO_CLIENT_ID=synthetic\nZOHO_CLIENT_SECRET=synthetic\nZOHO_REFRESH_TOKEN=synthetic\nZOHO_ORG_ID=synthetic\nZOHO_DEPARTMENT_ID=synthetic\nENABLE_JEV=false\nENABLE_BUB=false\nENABLE_WECOM=false\n"
	for _, tc := range []struct {
		name, setting, want string
		invalid             bool
	}{
		{"local default", "", "127.0.0.1", false},
		{"container", "APP_LISTEN_HOST=0.0.0.0\n", "0.0.0.0", false},
		{"URL is not an IP", "APP_LISTEN_HOST=http://localhost:8080\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(base+tc.setting), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid listen address accepted")
				}
				return
			}
			if err != nil || cfg.ListenHost != tc.want {
				t.Fatalf("listen configuration: %q %v", cfg.ListenHost, err)
			}
		})
	}
}
