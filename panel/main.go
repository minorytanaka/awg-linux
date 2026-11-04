// awg-panel: маленькое окно для туннеля AmneziaWG (awg-quick@awg0) - вкл/выкл и статистика.
package main

import (
	"fmt"
	"image/color"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const (
	iface       = "awg0"
	unit        = "awg-quick@" + iface + ".service"
	sysDir      = "/sys/class/net/" + iface
	handshakeOK = 180 * time.Second // рукопожатие свежее этого значит, что связь есть
)

var (
	green  = color.NRGBA{R: 0x4c, G: 0xaf, B: 0x7d, A: 0xff}
	yellow = color.NRGBA{R: 0xe5, G: 0xb8, B: 0x4b, A: 0xff}
	gray   = color.NRGBA{R: 0x8a, G: 0x8d, B: 0x99, A: 0xff}
)

func ifaceUp() bool {
	_, err := os.Stat(sysDir)
	return err == nil
}

func readCounter(name string) uint64 {
	b, err := os.ReadFile(sysDir + "/statistics/" + name)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v
}

// awgShow возвращает вывод `awg show awg0 <field>`; ok=false, если sudo без пароля не настроен.
func awgShow(field string) (string, bool) {
	out, err := exec.Command("sudo", "-n", "awg", "show", iface, field).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return ""
	}
	return f[len(f)-1]
}

func fmtBytes(n float64) string {
	for _, u := range []string{"Б", "КБ", "МБ", "ГБ"} {
		if n < 1024 {
			return fmt.Sprintf("%.1f %s", n, u)
		}
		n /= 1024
	}
	return fmt.Sprintf("%.1f ТБ", n)
}

func fmtAgo(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%d с назад", s)
	case s < 3600:
		return fmt.Sprintf("%d мин назад", s/60)
	default:
		return fmt.Sprintf("%d ч назад", s/3600)
	}
}

type snapshot struct {
	up                bool
	status            string
	color             color.Color
	handshake         string
	endpoint          string
	rx, tx            uint64
	rxRate, txRate    float64
}

type panel struct {
	busy atomic.Bool

	mu     sync.Mutex
	errMsg string
	ipText string

	// используются только горутиной-тикером
	prevT          time.Time
	prevRx, prevTx uint64

	status *canvas.Text
	button *widget.Button
	info   *widget.Label
	ip     *widget.Label
	errLbl *widget.Label
}

func (p *panel) setErr(s string) { p.mu.Lock(); p.errMsg = s; p.mu.Unlock() }
func (p *panel) setIP(s string)  { p.mu.Lock(); p.ipText = s; p.mu.Unlock() }

// collect собирает состояние туннеля; вызывается раз в секунду из фоновой горутины.
func (p *panel) collect() snapshot {
	now := time.Now()
	s := snapshot{up: ifaceUp(), handshake: "-", endpoint: "-"}
	s.rx, s.tx = readCounter("rx_bytes"), readCounter("tx_bytes")

	if dt := now.Sub(p.prevT).Seconds(); dt > 0 {
		if s.rx >= p.prevRx {
			s.rxRate = float64(s.rx-p.prevRx) / dt
		}
		if s.tx >= p.prevTx {
			s.txRate = float64(s.tx-p.prevTx) / dt
		}
	}
	p.prevT, p.prevRx, p.prevTx = now, s.rx, s.tx

	switch {
	case p.busy.Load():
		s.status, s.color = "...", gray
	case !s.up:
		s.status, s.color = "Выключено", gray
	default:
		hs, ok := awgShow("latest-handshakes")
		if !ok {
			s.status, s.color, s.handshake = "Включено", green, "нет прав (sudoers)"
			break
		}
		ts, _ := strconv.ParseInt(lastField(hs), 10, 64)
		if ts == 0 {
			s.status, s.color, s.handshake = "Нет связи", yellow, "ни разу"
		} else {
			age := time.Since(time.Unix(ts, 0))
			s.handshake = fmtAgo(age)
			if age < handshakeOK {
				s.status, s.color = "Подключено", green
			} else {
				s.status, s.color = "Нет связи", yellow
			}
		}
		if ep, ok := awgShow("endpoints"); ok && lastField(ep) != "" {
			s.endpoint = lastField(ep)
		}
	}
	return s
}

// render обновляет виджеты; вызывается только через fyne.Do.
func (p *panel) render(s snapshot) {
	p.status.Text = s.status
	p.status.Color = s.color
	p.status.Refresh()

	if s.up {
		p.button.SetText("Выключить")
		p.button.Importance = widget.DangerImportance
	} else {
		p.button.SetText("Включить")
		p.button.Importance = widget.SuccessImportance
	}
	if p.busy.Load() {
		p.button.Disable()
	} else {
		p.button.Enable()
	}
	p.button.Refresh()

	p.info.SetText(fmt.Sprintf(
		"Рукопожатие: %s\nСервер:      %s\nПринято:     %s\nОтправлено:  %s\nСкачивание:  %s/с\nОтдача:      %s/с",
		s.handshake, s.endpoint, fmtBytes(float64(s.rx)), fmtBytes(float64(s.tx)),
		fmtBytes(s.rxRate), fmtBytes(s.txRate),
	))

	p.mu.Lock()
	ipText, errMsg := p.ipText, p.errMsg
	p.mu.Unlock()
	p.ip.SetText(ipText)
	p.errLbl.SetText(errMsg)
}

func (p *panel) toggle() {
	if !p.busy.CompareAndSwap(false, true) {
		return
	}
	action := "start"
	if ifaceUp() {
		action = "stop"
	}
	p.setErr("")
	go func() {
		defer p.busy.Store(false)
		out, err := exec.Command("systemctl", action, unit).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			p.setErr(msg)
		}
	}()
}

func (p *panel) checkIP() {
	p.setIP("IP: проверяю...")
	go func() {
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("https://api.ipify.org")
		if err != nil {
			p.setIP("IP: ошибка")
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		if err != nil {
			p.setIP("IP: ошибка")
			return
		}
		p.setIP("IP: " + strings.TrimSpace(string(b)))
	}()
}

func main() {
	a := app.NewWithID("local.awgpanel")
	w := a.NewWindow("AWG")

	p := &panel{
		ipText: "IP: не проверялся",
		prevT:  time.Now(),
		prevRx: readCounter("rx_bytes"),
		prevTx: readCounter("tx_bytes"),
	}

	p.status = canvas.NewText("...", gray)
	p.status.TextSize = 24
	p.status.TextStyle = fyne.TextStyle{Bold: true}
	p.status.Alignment = fyne.TextAlignCenter

	p.button = widget.NewButton("...", p.toggle)

	p.info = widget.NewLabel("")
	p.info.TextStyle = fyne.TextStyle{Monospace: true}

	p.ip = widget.NewLabel("")
	p.ip.Alignment = fyne.TextAlignCenter

	p.errLbl = widget.NewLabel("")
	p.errLbl.Wrapping = fyne.TextWrapWord
	p.errLbl.Importance = widget.DangerImportance

	w.SetContent(container.NewPadded(container.NewVBox(
		p.status,
		p.button,
		widget.NewSeparator(),
		p.info,
		widget.NewButton("Проверить внешний IP", p.checkIP),
		p.ip,
		p.errLbl,
	)))
	w.Resize(fyne.NewSize(340, 420))

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			s := p.collect()
			fyne.Do(func() { p.render(s) })
			<-ticker.C
		}
	}()

	w.ShowAndRun()
}
