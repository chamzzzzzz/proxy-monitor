package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
)

var (
	proxies    []string
	availables = make(map[string]bool)
	smtpAddr   = os.Getenv("PROXY_MONITOR_SMTP_ADDR")
	smtpUser   = os.Getenv("PROXY_MONITOR_SMTP_USER")
	smtpPass   = os.Getenv("PROXY_MONITOR_SMTP_PASS")
	source     = "From: {{.From}}\r\nTo: {{.To}}\r\nSubject: {{.Subject}}\r\n\r\n{{.Body}}"
	t          *template.Template
)

func main() {
	proxiesEnv := os.Getenv("PROXY_MONITOR_PROXIES")
	if proxiesEnv == "" {
		for i := 1082; i <= 1087; i++ {
			proxies = append(proxies, fmt.Sprintf("socks5://127.0.0.1:%d", i))
		}
	} else {
		proxies = strings.Split(proxiesEnv, ",")
	}
	for _, proxy := range proxies {
		availables[proxy] = true
	}

	funcs := template.FuncMap{
		"bencoding": mime.BEncoding.Encode,
	}
	t = template.Must(template.New("mail").Funcs(funcs).Parse(source))

	for {
		check()
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, now.Location())
		log.Printf("next check at %s\n", next.Format("2006-01-02 15:04:05"))
		time.Sleep(next.Sub(now))
	}
}

func check() {
	log.Printf("start check at %s\n", time.Now().Format("2006-01-02 15:04:05"))
	t := time.Now()

	changeset := make(map[string]bool)
	for _, proxy := range proxies {
		if proxy == "" {
			continue
		}
		_, err := testing(proxy)
		available := err == nil
		if availables[proxy] != available {
			availables[proxy], changeset[proxy] = available, available
		}
	}
	unavailable := false
	for proxy, available := range changeset {
		if available {
			log.Printf("proxy %s change to available\n", proxy)
		} else {
			log.Printf("proxy %s change to unavailable\n", proxy)
		}
	}
	for proxy, available := range availables {
		if !available {
			if _, ok := changeset[proxy]; !ok {
				log.Printf("proxy %s is still unavailable\n", proxy)
			}
			unavailable = true
		}
	}
	if unavailable {
		notification(changeset, availables)
	}
	log.Printf("check used %v\n", time.Since(t))
	log.Printf("finish check at %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

func testing(proxy string) ([]byte, error) {
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	resp, err := client.Get("https://ifconfig.me/ip")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func notification(changeset, availables map[string]bool) {
	type Data struct {
		From       string
		To         string
		Subject    string
		Body       string
		Changeset  map[string]bool
		Availables map[string]bool
	}

	log.Printf("sending notification...")
	addr := smtpAddr
	if addr == "" {
		addr = "smtp.mail.me.com:587"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		log.Printf("send notification fail. err='%s'\n", err)
		return
	}
	user := smtpUser
	password := smtpPass
	to := []string{user}

	body := ""
	subject := ""
	if len(changeset) > 0 {
		subject = "状态变更"
		for proxy, available := range changeset {
			body += fmt.Sprintf("%s %s\r\n", proxy, desc(available))
		}
	} else {
		subject = "状态持续"
		for proxy, available := range availables {
			if !available {
				body += fmt.Sprintf("%s %s\r\n", proxy, desc(available))
			}
		}
	}

	data := Data{
		From:       fmt.Sprintf("%s <%s>", mime.BEncoding.Encode("UTF-8", "Proxy Monitor"), user),
		To:         to[0],
		Subject:    mime.BEncoding.Encode("UTF-8", fmt.Sprintf("Proxy-%s", subject)),
		Body:       body,
		Changeset:  changeset,
		Availables: availables,
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Printf("send notification fail. err='%s'\n", err)
		return
	}

	auth := smtp.PlainAuth("", user, password, host)
	if err := smtp.SendMail(addr, auth, user, to, buf.Bytes()); err != nil {
		log.Printf("send notification fail. err='%s'\n", err)
	}
	log.Printf("send notification success.\n")
}

func desc(available bool) string {
	if available {
		return "可用"
	}
	return "不可用"
}
