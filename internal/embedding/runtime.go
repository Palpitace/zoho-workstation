// input: 固定 GGUF 文件、随包 CPU llama-server 及本地检索文本。
// output: 可取消下载、校验、本机认证子进程和归一化 1024 维向量。
// pos: 应用管理的本地嵌入运行时。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package embedding

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const QueryVersion = "qwen-websearch-v1"

const ModelID = "qwen3-0.6b-q4km-imat:4d0f2e6e4132e96a0:1024"
const ModelSHA = "4d0f2e6e4132e96a0a0eac04cebfd11ff08cee89d3128aba6b276ae06f808c30"
const ModelURL = "https://huggingface.co/PeterAM4/Qwen3-Embedding-0.6B-GGUF/resolve/bb661aeeeafa4ff7303b5f5e1e80255efd6a513c/Qwen3-Embedding-0.6B-Q4_K_M-imat.gguf"
const ModelBytes int64 = 396474336

type Status struct {
	State    string `json:"state"`
	Progress int    `json:"progress"`
	Error    string `json:"error,omitempty"`
	Degraded bool   `json:"degraded"`
	Model    string `json:"model"`
}
type Runtime struct {
	mu            sync.Mutex
	call          sync.Mutex
	status        Status
	cancel        context.CancelFunc
	done          chan struct{}
	process       *exec.Cmd
	processDone   chan struct{}
	endpoint, key string
	dir           string
	closed        bool
}

func New(dir string) *Runtime {
	return &Runtime{dir: dir, status: Status{State: "not_installed", Degraded: true, Model: ModelID}}
}
func (r *Runtime) Status() Status { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *Runtime) update(state, message string, progress int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = Status{State: state, Error: message, Progress: progress, Degraded: state != "ready", Model: ModelID}
}
func (r *Runtime) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("service closed")
	}
	if r.done != nil || r.status.State == "ready" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	done := make(chan struct{})
	r.done = done
	r.status.State = "downloading"
	r.status.Error = ""
	r.status.Degraded = true
	go func() {
		defer close(done)
		err := r.prepare(ctx)
		if err != nil {
			r.update("failed", err.Error(), 0)
		}
		r.mu.Lock()
		r.done = nil
		r.mu.Unlock()
	}()
	return nil
}
func (r *Runtime) Close() {
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	done := r.done
	r.mu.Unlock()
	// Initialization can install its process after cancellation. Wait for it before
	// taking the final process handle, then reap the child before returning.
	if done != nil {
		<-done
	}
	r.mu.Lock()
	cmd, pd := r.process, r.processDone
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if pd != nil {
		<-pd
	}
	r.update("stopped", "", 0)
}
func (r *Runtime) prepare(ctx context.Context) error {
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return errors.New("无法创建模型目录")
	}
	path := filepath.Join(r.dir, "qwen3-embedding.gguf")
	if !validFile(path) {
		if err := r.download(ctx, path); err != nil {
			return err
		}
	}
	r.update("starting", "", 100)
	executable := filepath.Join("runtime", "llama", "llama-server")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if _, err := os.Stat(executable); err != nil {
		return errors.New("缺少本地推理程序，请使用完整下载包")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("本机推理端口不可用")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	key := hex.EncodeToString(token)
	keyFile, err := os.CreateTemp(r.dir, ".runtime-key-*")
	if err != nil {
		return errors.New("无法创建本机推理凭据")
	}
	keyPath := keyFile.Name()
	defer os.Remove(keyPath)
	if _, err = keyFile.WriteString(key); err != nil {
		keyFile.Close()
		return errors.New("无法保存本机推理凭据")
	}
	if err = keyFile.Close(); err != nil {
		return errors.New("无法保存本机推理凭据")
	}

	cmd := exec.CommandContext(ctx, executable, "--model", path, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--embedding", "--pooling", "last", "--ctx-size", "8192", "--batch-size", "8192", "--ubatch-size", "8192", "--parallel", "1", "--threads", fmt.Sprint(min(runtime.NumCPU(), 8)), "--n-gpu-layers", "0", "--api-key-file", keyPath, "--no-webui")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return errors.New("本地推理程序无法启动")
	}
	pd := make(chan struct{})
	r.mu.Lock()
	r.process = cmd
	r.processDone = pd
	r.endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
	r.key = key
	r.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		r.mu.Lock()
		if r.process == cmd {
			r.status.State = "stopped"
			r.status.Degraded = true
		}
		r.mu.Unlock()
		close(pd)
	}()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("模型启动已取消")
		case <-pd:
			return errors.New("本地推理进程提前退出")
		case <-timer.C:
			_ = cmd.Process.Kill()
			return errors.New("本地模型启动超时")
		case <-ticker.C:
			req, _ := http.NewRequestWithContext(ctx, "GET", r.endpoint+"/health", nil)
			req.Header.Set("Authorization", "Bearer "+key)
			client := http.Client{Timeout: time.Second}
			resp, e := client.Do(req)
			if e == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					r.update("ready", "", 100)
					return nil
				}
			}
		}
	}
}
func validFile(path string) bool {
	f, e := os.Open(path)
	if e != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return e == nil && n == ModelBytes && hex.EncodeToString(h.Sum(nil)) == ModelSHA
}
func (r *Runtime) download(ctx context.Context, path string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", ModelURL, nil)
	client := http.Client{Timeout: 30 * time.Minute}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("模型下载失败，可重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("模型下载服务暂不可用")
	}
	tmp := path + ".part"
	defer os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("无法保存模型")
	}
	h := sha256.New()
	buf := make([]byte, 256*1024)
	var total int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > ModelBytes {
				f.Close()
				return errors.New("模型大小校验失败")
			}
			if _, e = io.MultiWriter(f, h).Write(buf[:n]); e != nil {
				f.Close()
				return errors.New("模型写入失败")
			}
			r.update("downloading", "", int(total*100/ModelBytes))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return errors.New("模型下载中断，可重试")
		}
	}
	if e = f.Close(); e != nil {
		return errors.New("模型保存失败")
	}
	if total != ModelBytes || hex.EncodeToString(h.Sum(nil)) != ModelSHA {
		return errors.New("模型校验失败，请重试")
	}
	if e = os.Rename(tmp, path); e != nil {
		return errors.New("模型安装失败")
	}
	return nil
}
func (r *Runtime) Embed(ctx context.Context, text string, query bool) ([]float32, error) {
	r.call.Lock()
	defer r.call.Unlock()
	r.mu.Lock()
	state, url, key := r.status.State, r.endpoint, r.key
	r.mu.Unlock()
	if state != "ready" {
		return nil, errors.New("local embedding unavailable")
	}
	if len(text) == 0 || len(text) > 24*1024 {
		return nil, errors.New("embedding text out of bounds")
	}
	if query {
		text = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: " + text
	}
	body, _ := json.Marshal(map[string]any{"input": text, "encoding_format": "float"})
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url+"/v1/embeddings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("local embedding failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("local embedding rejected input")
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&result) != nil || len(result.Data) != 1 {
		return nil, errors.New("invalid embedding result")
	}
	v := result.Data[0].Embedding
	if len(v) != 1024 {
		return nil, errors.New("invalid embedding dimension")
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, errors.New("nonfinite embedding")
		}
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return nil, errors.New("empty embedding")
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(norm))
	}
	return v, nil
}
