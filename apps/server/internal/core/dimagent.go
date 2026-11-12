package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type dimToken struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Expires int64  `json:"expires"`
}

var dimRefresh struct {
	sync.Mutex
	until time.Time
	err   error
	path  string
}

func dimAuthPath() (string, error) {
	home := os.Getenv("DIMCODE_HOME")
	if home == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		home = filepath.Join(h, ".dimcode", "v2")
	}
	return filepath.Join(home, "auth.json"), nil
}
func readDimAuth() (dimToken, error) {
	path, err := dimAuthPath()
	if err != nil {
		return dimToken{}, errors.New("无法定位 DimAgent 授权文件")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return dimToken{}, fmt.Errorf("未找到 DimAgent 授权文件（%s），请先运行 dim auth login", path)
	}
	if err != nil {
		return dimToken{}, errors.New("无法读取 DimAgent 授权文件，请检查文件权限")
	}
	var data struct {
		OAuth dimToken `json:"nextApiOauth"`
	}
	err = json.Unmarshal(raw, &data)
	if err != nil {
		time.Sleep(100 * time.Millisecond)
		raw, err = os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(raw, &data)
		}
	}
	if err != nil || data.OAuth.Type != "oauth" || data.OAuth.Access == "" {
		return dimToken{}, errors.New("DimAgent 授权文件无法解析，请重新运行 dim auth login")
	}
	return data.OAuth, nil
}
func dimAuthState() (string, string) {
	tok, err := readDimAuth()
	if err != nil {
		path, _ := dimAuthPath()
		if _, e := os.Stat(path); os.IsNotExist(e) {
			return "missing", "未找到授权文件，请先运行 dim auth login"
		}
		return "invalid", err.Error()
	}
	if tok.Expires <= time.Now().UnixMilli() {
		return "expired", "授权已过期，请求时会尝试自动刷新"
	}
	return "ok", ""
}
func (s *Server) credential(c Channel) (string, error) {
	if c.AuthMode != "cli_oauth" {
		return c.APIKey, nil
	}
	tok, err := readDimAuth()
	if err != nil {
		return "", err
	}
	if tok.Expires > time.Now().Add(25*time.Second).UnixMilli() {
		return tok.Access, nil
	}
	dimRefresh.Lock()
	defer dimRefresh.Unlock()
	tok, err = readDimAuth()
	if err != nil {
		return "", err
	}
	if tok.Expires > time.Now().Add(25*time.Second).UnixMilli() {
		return tok.Access, nil
	}
	path, _ := dimAuthPath()
	if path == dimRefresh.path && time.Now().Before(dimRefresh.until) {
		return "", dimRefresh.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dim", "auth", "refresh")
	err = cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errors.New("未找到 dim 命令，无法刷新 DimAgent 授权")
		} else {
			err = errors.New("DimAgent 授权刷新失败，请运行 dim auth refresh 检查登录状态")
		}
	} else {
		tok, err = readDimAuth()
		if err == nil && tok.Expires <= time.Now().Add(25*time.Second).UnixMilli() {
			err = errors.New("DimAgent 授权刷新后仍已过期，请重新登录")
		}
	}
	if err != nil {
		dimRefresh.path = path
		dimRefresh.until = time.Now().Add(30 * time.Second)
		dimRefresh.err = err
		return "", err
	}
	dimRefresh.until = time.Time{}
	return tok.Access, nil
}

// DimAgent's OAuth relay recognizes requests using the CLI's public identity headers.
func applyDimOAuthHeaders(req *http.Request, c Channel) {
	if c.Kind != "dimagent" || c.AuthMode != "cli_oauth" {
		return
	}
	req.Header.Set("User-Agent", "DimAgent/0.5.27")
	req.Header.Set("X-Title", "DimCode")
	req.Header.Set("HTTP-Referer", "https://dimagent.com/")
}
