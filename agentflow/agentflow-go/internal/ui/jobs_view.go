package ui

import (
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"agentflow-go/internal/core"
	"agentflow-go/internal/model"
)

type jobsView struct {
	app  *core.App
	list *widget.List
	jobs []model.Job
}

func newJobsView(a *core.App) *jobsView {
	return &jobsView{app: a}
}

func (jv *jobsView) content() fyne.CanvasObject {
	statsLabel := widget.NewLabel("")
	jv.list = widget.NewList(
		func() int { return len(jv.jobs) },
		func() fyne.CanvasObject {
			return container.NewGridWithColumns(3,
				widget.NewLabel("type"),
				widget.NewProgressBar(),
				widget.NewLabel("status"),
			)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(jv.jobs) {
				return
			}
			j := jv.jobs[id]
			cols := item.(*fyne.Container).Objects
			cols[0].(*widget.Label).SetText(string(j.Type))
			bar := cols[1].(*widget.ProgressBar)
			bar.SetValue(float64(j.Progress) / 100.0)
			statusText := string(j.Status)
			if j.Error != "" {
				statusText = "failed: " + j.Error
			}
			cols[2].(*widget.Label).SetText(statusText)
		},
	)

	jv.refreshStats(statsLabel)

	header := container.NewVBox(
		widget.NewLabel("Background Jobs"),
		statsLabel,
		widget.NewSeparator(),
	)

	return container.NewBorder(header, nil, nil, nil, jv.list)
}

func (jv *jobsView) refreshStats(lbl *widget.Label) {
	stats := jv.app.WorkerStats()
	active, _ := stats["active_workers"].(int64)
	queued, _ := stats["queue_length"].(int)
	completed, _ := stats["completed"].(int64)
	lbl.SetText(fmt.Sprintf("Workers: %d active  |  Queue: %d  |  Completed: %d",
		active, queued, completed))
}

func (jv *jobsView) refresh() {
	jv.jobs = jv.app.GetJobs()
	sort.Slice(jv.jobs, func(i, j int) bool {
		return jv.jobs[i].CreatedAt.After(jv.jobs[j].CreatedAt)
	})
	if jv.list != nil {
		jv.list.Refresh()
	}
}
