package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"agentflow-go/internal/core"
	"agentflow-go/internal/model"
)

// Run initialises the Fyne application and blocks until the window is closed.
func Run(a *core.App) {
	fyneApp := app.NewWithID("com.agentflow.legal")
	fyneApp.Settings().SetTheme(theme.DarkTheme())

	w := fyneApp.NewWindow("AgentFlow — Legal AI")
	w.Resize(fyne.NewSize(1280, 800))
	w.SetMaster()

	cv := newCasesView(a, w)
	jv := newJobsView(a)
	sv := newSettingsView(a)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Cases", theme.FolderOpenIcon(), cv.content()),
		container.NewTabItemWithIcon("Jobs", theme.MediaPlayIcon(), jv.content()),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), sv.content()),
	)
	tabs.SetTabLocation(container.TabLocationLeading)

	statusLabel := widget.NewLabel("AgentFlow ready")
	root := container.NewBorder(nil, statusLabel, nil, nil, tabs)
	w.SetContent(root)

	a.Subscribe(func() {
		cv.refresh()
		jv.refresh()
		jobs := a.GetJobs()
		n := countActive(jobs)
		if n > 0 {
			statusLabel.SetText(fmt.Sprintf("Processing %s…", pluralJobs(n)))
		} else {
			statusLabel.SetText("AgentFlow ready")
		}
	})

	w.ShowAndRun()
}

func countActive(jobs []model.Job) int {
	n := 0
	for _, j := range jobs {
		if j.Status == model.JobStatusProcessing || j.Status == model.JobStatusPending {
			n++
		}
	}
	return n
}

func pluralJobs(n int) string {
	if n == 1 {
		return "1 job"
	}
	return fmt.Sprintf("%d jobs", n)
}
