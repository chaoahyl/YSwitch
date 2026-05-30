package main

import "sync"

var (
	appLocale   = "zh"
	appLocaleMu sync.RWMutex
)

func (a *App) SetLocale(lang string) {
	appLocaleMu.Lock()
	defer appLocaleMu.Unlock()
	if lang == "en" || lang == "zh" {
		appLocale = lang
	}
}

// tr returns the Chinese or English string based on the current locale.
func tr(zh, en string) string {
	appLocaleMu.RLock()
	defer appLocaleMu.RUnlock()
	if appLocale == "en" {
		return en
	}
	return zh
}
