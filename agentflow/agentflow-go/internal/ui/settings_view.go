package ui

import (
	"fmt"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"agentflow-go/internal/core"
)

type settingsView struct {
	app *core.App
}

func newSettingsView(a *core.App) *settingsView {
	return &settingsView{app: a}
}

func (sv *settingsView) content() fyne.CanvasObject {
	cfg := sv.app.Cfg

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetText(cfg.DashScopeAPIKey)
	apiKeyEntry.SetPlaceHolder("sk-…")
	apiKeyEntry.Disable()

	modelEntry := widget.NewEntry()
	modelEntry.SetText(cfg.ModelName)
	modelEntry.Disable()

	backendLabel := widget.NewLabel(cfg.LLMBackend)
	dataDirLabel := widget.NewLabel(cfg.DataDir)
	dataDirLabel.Wrapping = fyne.TextWrapBreak

	workerStats := sv.app.WorkerStats()
	statsText := fmt.Sprintf(
		"Workers: %v\nActive: %v\nCompleted: %v\nFailed: %v",
		workerStats["num_workers"],
		workerStats["active_workers"],
		workerStats["completed"],
		workerStats["failed"],
	)
	statsLabel := widget.NewLabel(statsText)

	sysInfo := fmt.Sprintf(
		"OS: %s/%s\nCPUs: %d\nGo: %s",
		runtime.GOOS, runtime.GOARCH,
		runtime.NumCPU(),
		runtime.Version(),
	)
	sysLabel := widget.NewLabel(sysInfo)

	form := widget.NewForm(
		widget.NewFormItem("LLM Backend", backendLabel),
		widget.NewFormItem("Model", modelEntry),
		widget.NewFormItem("API Key", apiKeyEntry),
		widget.NewFormItem("Data Directory", dataDirLabel),
	)

	note := widget.NewLabel("Configuration is loaded from environment variables.\nEdit them in your shell profile or .env file to make changes.")
	note.Wrapping = fyne.TextWrapWord

	return container.NewVScroll(container.NewVBox(
		widget.NewRichTextFromMarkdown("## Settings"),
		form,
		widget.NewSeparator(),
		widget.NewLabel("Worker Stats"),
		statsLabel,
		widget.NewSeparator(),
		widget.NewLabel("System Info"),
		sysLabel,
		widget.NewSeparator(),
		note,
	))
}
