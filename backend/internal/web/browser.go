package web

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// OpenLocalBrowserWhenReady 在固定监听端口就绪后打开本机浏览器。
//
// 这里只解析启动参数/配置合成后的监听地址，不改变 HTTP Server 的监听方式；
// 端口为 0 时无法从配置得到真实端口，因此跳过自动打开。等待探测放在调用方
// goroutine 中，浏览器启动失败也不会阻塞 zacp 服务。
func OpenLocalBrowserWhenReady(listenAddr string, timeout time.Duration) (string, error) {
	url, probeAddr, ok, err := localBrowserTarget(listenAddr)
	if err != nil || !ok {
		return "", err
	}

	program, args, ok := browserCommand(url)
	if !ok {
		return "", nil
	}

	if err := waitForListener(probeAddr, timeout); err != nil {
		return url, fmt.Errorf("wait for HTTP listener %s: %w", probeAddr, err)
	}

	cmd := exec.Command(program, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return url, fmt.Errorf("start browser: %w", err)
	}
	// 浏览器启动器通常很快退出；回收子进程，避免 Unix 上留下 zombie。
	go func() { _ = cmd.Wait() }()
	return url, nil
}

func localBrowserTarget(listenAddr string) (url, probeAddr string, ok bool, err error) {
	host, portText, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", "", false, fmt.Errorf("parse listen address %q: %w", listenAddr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return "", "", false, fmt.Errorf("invalid listen port %q", portText)
	}
	if port == 0 {
		return "", "", false, nil
	}

	// localhost URL 只对本机监听地址有意义；绑定到指定远程网卡时不自动打开，
	// 避免启动后弹出一个必然无法访问的 localhost 页面。
	if !isLocalBindHost(host) {
		return "", "", false, nil
	}

	portText = strconv.Itoa(port)
	url = "http://localhost:" + portText + "/"
	probeHost := host
	if probeHost == "" || probeHost == "0.0.0.0" || probeHost == "::" || probeHost == "localhost" {
		probeHost = "127.0.0.1"
	}
	return url, net.JoinHostPort(probeHost, portText), true, nil
}

func isLocalBindHost(host string) bool {
	if host == "" || host == "0.0.0.0" || host == "::" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func browserCommand(url string) (program string, args []string, ok bool) {
	return browserCommandForPlatform(runtime.GOOS, url)
}

func browserCommandForPlatform(platform, url string) (program string, args []string, ok bool) {
	switch platform {
	case "darwin":
		return "open", []string{url}, true
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}, true
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return "", nil, false
		}
		return "xdg-open", []string{url}, true
	default:
		return "", nil, false
	}
}

func waitForListener(addr string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
