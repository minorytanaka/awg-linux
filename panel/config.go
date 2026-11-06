package main

// Чистка и проверка конфига перед заменой. Правила совпадают с
// packaging/awg-panel-set-config: при изменении менять в обоих местах.
// Здесь проверка нужна, чтобы показать ошибку до запроса пароля;
// записывает конфиг только хелпер, и он проверяет все заново.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}

	// sed -E '/^[[:space:]]*[A-Za-z0-9]+[[:space:]]*=[[:space:]]*$/d'
	emptyParamRe = regexp.MustCompile(`^[[:space:]]*[A-Za-z0-9]+[[:space:]]*=[[:space:]]*$`)

	// grep -qiE, построчно
	requiredRes = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"[Interface]", regexp.MustCompile(`(?i)^[[:space:]]*\[Interface\][[:space:]]*(#.*)?$`)},
		{"[Peer]", regexp.MustCompile(`(?i)^[[:space:]]*\[Peer\][[:space:]]*(#.*)?$`)},
		{"PrivateKey", regexp.MustCompile(`(?i)^[[:space:]]*PrivateKey[[:space:]]*=[[:space:]]*[^[:space:]#]`)},
		{"PublicKey", regexp.MustCompile(`(?i)^[[:space:]]*PublicKey[[:space:]]*=[[:space:]]*[^[:space:]#]`)},
		{"Endpoint", regexp.MustCompile(`(?i)^[[:space:]]*Endpoint[[:space:]]*=[[:space:]]*[^[:space:]#]`)},
	}

	// команды, которые awg-quick выполнит от root; не ошибка, только предупреждение
	hookRe = regexp.MustCompile(`(?i)^[[:space:]]*(PreUp|PostUp|PreDown|PostDown)[[:space:]]*=`)
)

// cleanConfig повторяет sed из хелпера:
//  1. UTF-8 BOM в самом начале файла;
//  2. один CR в конце каждой строки;
//  3. строки "Ключ =" без значения.
//
// Больше ничего не меняет.
func cleanConfig(raw []byte) []byte {
	b := bytes.TrimPrefix(raw, utf8BOM)
	lines := bytes.Split(b, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, l := range lines {
		l = bytes.TrimSuffix(l, []byte("\r"))
		if emptyParamRe.Match(l) {
			continue
		}
		out = append(out, l)
	}
	return bytes.Join(out, []byte("\n"))
}

// checkConfig проверяет сырой конфиг так же, как хелпер. Текст ошибки не содержит
// содержимого конфига.
func checkConfig(raw []byte) error {
	switch {
	case len(raw) == 0:
		return errors.New("Конфиг пустой")
	case len(raw) > maxConfigSize:
		return errors.New("Конфиг больше 16 КБ, это не похоже на конфиг AmneziaWG")
	case bytes.IndexByte(raw, 0) >= 0:
		return errors.New("Файл бинарный (есть нулевые байты), это не конфиг")
	}
	clean := cleanConfig(raw)
	defer clear(clean)

	found := make([]bool, len(requiredRes))
	for _, l := range bytes.Split(clean, []byte("\n")) {
		for i, r := range requiredRes {
			if !found[i] && r.re.Match(l) {
				found[i] = true
			}
		}
	}
	var missing []string
	for i, r := range requiredRes {
		if !found[i] {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("В конфиге нет: %s", strings.Join(missing, " "))
	}
	return nil
}

// hasHooks: есть ли в конфиге PreUp/PostUp/PreDown/PostDown.
func hasHooks(raw []byte) bool {
	for _, l := range bytes.Split(raw, []byte("\n")) {
		if hookRe.Match(l) {
			return true
		}
	}
	return false
}

func serviceActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

var errCancelled = errors.New("Отменено: пароль не введен, конфиг не изменен")

// runSetConfigHelper передает конфиг хелперу через stdin под pkexec.
// Без аргументов и без shell; содержимое конфига никуда не пишется.
func runSetConfigHelper(raw []byte) error {
	if _, err := os.Stat(setConfigHelper); err != nil {
		return fmt.Errorf("Не установлен %s.\nЗапусти ./scripts/install-panel.sh", setConfigHelper)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("pkexec", setConfigHelper)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	// сообщения pkexec на английском, чтобы их можно было распознать
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	err := cmd.Run()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(stderr.String())

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return fmt.Errorf("Не удалось запустить pkexec: %v", err)
	}
	switch code := ee.ExitCode(); {
	case code == 126:
		return errCancelled
	case code == 127 && strings.Contains(strings.ToLower(msg), "authentication agent"):
		return errors.New("Не запущен агент polkit, поэтому некому спросить пароль.\n" +
			"Перезайди в сессию или смени конфиг вручную (README, раздел про смену конфига).")
	case code == 127:
		return errors.New("Нет прав на смену конфига (pkexec отказал).\nКонфиг не изменен.")
	case code >= 2 && code <= 5 && msg != "":
		return fmt.Errorf("%s\nКонфиг не изменен.", msg)
	default:
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("Ошибка хелпера (код %d): %s", code, msg)
	}
}
