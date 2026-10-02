package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Enabled      bool   `json:"enabled"`
	Resolution   string `json:"resolution"` // "auto" or "WxH"
	FPS          int    `json:"fps"`        // 0 = follow tablet
	Quality      string `json:"quality"`    // "auto","low","medium","high","ultra"
	Scale        string `json:"scale"`      // "auto" or number
	Position     string `json:"position"`   // right,left,above,below
	AutoLaunch   bool   `json:"autoLaunch"`
	AutoInstall  bool   `json:"autoInstall"`
	Port         int    `json:"port"`
	RestoreToken string `json:"restoreToken,omitempty"`
}

var (
	cfg   = Config{Enabled: true, Resolution: "auto", FPS: 0, Quality: "auto", Scale: "auto", Position: "right", AutoLaunch: true, AutoInstall: true, Port: 27183}
	cfgMu sync.Mutex
)

func configPath() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "matlink", "config.json")
}

func loadConfig() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &cfg)
	}
}

func saveConfig() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath(), b, 0o600)
}

func getConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg
}

func updateConfig(f func(*Config)) {
	cfgMu.Lock()
	f(&cfg)
	cfgMu.Unlock()
	saveConfig()
}
