package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
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
	proxies  []string
	smtpAddr = os.Getenv("PROXY_MONITOR_SMTP_ADDR")
	smtpUser = os.Getenv("PROXY_MONITOR_SMTP_USER")
	smtpPass = os.Getenv("PROXY_MONITOR_SMTP_PASS")
	source   = "From: {{.From}}\r\nTo: {{.To}}\r\nSubject: {{.Subject}}\r\n\r\n{{.Body}}"
	t        *template.Template
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

	var unavailable []string
	for _, proxy := range proxies {
		if proxy == "" {
			continue
		}
		_, err := checkAvaiable(proxy)
		if err != nil {
			unavailable = append(unavailable, proxy)
		}
	}
	if len(unavailable) == 0 {
		log.Printf("all proxies are available\n")
	} else {
		log.Printf("unavailable proxies: %v\n", unavailable)
		notification(unavailable)
	}

	log.Printf("check used %v\n", time.Since(t))
	log.Printf("finish check at %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

func checkAvaiable(proxy string) ([]byte, error) {
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
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func notification(unavailable []string) {
	type Data struct {
		From        string
		To          string
		Subject     string
		Body        string
		Unavailable []string
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
	for _, proxy := range unavailable {
		body += fmt.Sprintf("%s\r\n", proxy)
	}

	data := Data{
		From:        fmt.Sprintf("%s <%s>", mime.BEncoding.Encode("UTF-8", "Proxy Monitor"), user),
		To:          to[0],
		Subject:     mime.BEncoding.Encode("UTF-8", fmt.Sprintf("Proxy-%s", "Unavailable")),
		Body:        body,
		Unavailable: unavailable,
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