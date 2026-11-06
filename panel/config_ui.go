package main

// Окно "Смена конфига": текст или .conf файл -> подтверждение -> хелпер через pkexec
// -> перезапуск туннеля, если он был включен.

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

// showConfigWindow открывает (или поднимает) окно смены конфига. Только из UI-потока.
func (p *panel) showConfigWindow() {
	if p.cfgWin != nil {
		p.cfgWin.RequestFocus()
		return
	}
	win := p.app.NewWindow("Смена конфига")
	entry := widget.NewMultiLineEntry()
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.Wrapping = fyne.TextWrapOff
	entry.SetPlaceHolder("[Interface]\nPrivateKey = ...\n...\n\n[Peer]\nPublicKey = ...\nEndpoint = ...")

	fromFile := widget.NewButton("Выбрать .conf файл...", func() { p.pickConfigFile(win) })
	apply := widget.NewButton("Применить", func() {
		p.confirmAndApply(win, []byte(entry.Text))
	})
	apply.Importance = widget.HighImportance

	win.SetContent(container.NewBorder(
		widget.NewLabel("Вставь текст конфига или выбери файл. Старый конфиг сохранится в awg0.conf.bak."),
		container.NewHBox(fromFile, layout.NewSpacer(), apply),
		nil, nil,
		entry,
	))
	win.SetOnClosed(func() {
		// не оставляем ключ в памяти виджета
		entry.SetText("")
		p.cfgWin, p.cfgEntry = nil, nil
	})
	p.cfgWin, p.cfgEntry = win, entry
	win.Resize(fyne.NewSize(560, 440))
	win.Show()
}

// pickConfigFile: диалог выбора .conf. Содержимое файла в поле ввода не попадает.
func (p *panel) pickConfigFile(win fyne.Window) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		if r == nil {
			return // отмена
		}
		defer r.Close()
		data, err := io.ReadAll(io.LimitReader(r, maxConfigSize+1))
		if err != nil {
			clear(data)
			dialog.ShowError(fmt.Errorf("Не удалось прочитать файл: %v", err), win)
			return
		}
		p.confirmAndApply(win, data)
	}, win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".conf"}))
	d.Resize(fyne.NewSize(620, 460))
	d.Show()
}

// confirmAndApply: предпроверка, диалог подтверждения, затем applyConfig.
// raw после использования обнуляется.
func (p *panel) confirmAndApply(win fyne.Window, raw []byte) {
	if p.busy.Load() {
		clear(raw)
		dialog.ShowError(errors.New("Подожди, туннель сейчас включается или выключается"), win)
		return
	}
	if err := checkConfig(raw); err != nil {
		clear(raw)
		dialog.ShowError(fmt.Errorf("%v\nКонфиг не изменен.", err), win)
		return
	}

	var msg strings.Builder
	msg.WriteString("Заменить /etc/amnezia/amneziawg/awg0.conf?\nСтарый сохранится в awg0.conf.bak.\n\n")
	switch {
	case serviceActive():
		msg.WriteString("Туннель включен и будет перезапущен с новым конфигом.")
	case ifaceUp():
		msg.WriteString("Туннель поднят вручную, не через панель. Его нужно будет перезапустить самому.")
	default:
		msg.WriteString("Туннель выключен и останется выключенным.")
	}
	if hasHooks(cleanConfig(raw)) {
		msg.WriteString("\n\nВнимание: в конфиге есть PreUp/PostUp/PreDown/PostDown.\n" +
			"Эти команды awg-quick выполнит от root. Применяй, только если конфиг из надежного источника.")
	}
	msg.WriteString("\n\nПотребуется пароль администратора.")

	c := dialog.NewConfirm("Смена конфига", msg.String(), func(ok bool) {
		if !ok {
			clear(raw)
			return
		}
		p.applyConfig(win, raw)
	}, win)
	c.SetConfirmText("Заменить")
	c.SetDismissText("Отмена")
	c.Show()
}

// applyConfig запускает хелпер и перезапуск в фоне; результат показывает в окне.
func (p *panel) applyConfig(win fyne.Window, raw []byte) {
	if !p.busy.CompareAndSwap(false, true) {
		clear(raw)
		dialog.ShowError(errors.New("Подожди, туннель сейчас включается или выключается"), win)
		return
	}
	p.setErr("")
	prog := dialog.NewCustomWithoutButtons("Смена конфига",
		container.NewVBox(widget.NewLabel("Меняю конфиг..."), widget.NewProgressBarInfinite()), win)
	prog.Show()

	go func() {
		defer p.busy.Store(false)

		wasActive := serviceActive()
		manual := !wasActive && ifaceUp()

		err := runSetConfigHelper(raw)
		clear(raw)
		replaced := err == nil

		note := ""
		switch {
		case err != nil:
		case wasActive:
			out, rerr := exec.Command("systemctl", "restart", unit).CombinedOutput()
			if rerr != nil {
				m := strings.TrimSpace(string(out))
				if m == "" {
					m = rerr.Error()
				}
				err = fmt.Errorf("Конфиг заменен (старый в awg0.conf.bak), но туннель не перезапустился:\n%s", m)
				p.setErr("Туннель не перезапустился после смены конфига")
			} else {
				note = "Туннель перезапущен с новым конфигом."
			}
		case manual:
			note = "Туннель поднят вручную. Чтобы применить конфиг:\n" +
				"sudo awg-quick down " + iface + "\nи включи туннель кнопкой в панели."
		default:
			note = "Туннель выключен и остался выключенным."
		}

		fyne.Do(func() {
			prog.Hide()
			parent := win
			if p.cfgWin != win { // окно смены конфига успели закрыть
				parent = p.mainWin
			}
			if replaced && p.cfgWin == win && p.cfgEntry != nil { // после успеха поле всегда пустое
				p.cfgEntry.SetText("")
			}
			if err != nil {
				dialog.ShowError(err, parent)
				return
			}
			dialog.ShowInformation("Готово",
				"Конфиг заменен, старый сохранен в awg0.conf.bak.\n"+note, parent)
		})
	}()
}
