package worker

import (
	"container/heap"
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"agentflow-go/internal/model"
)

type poolJob struct {
	job      *model.Job
	fn       func(job *model.Job) (any, error)
	priority int
	index    int
}

type priorityQueue []*poolJob

func (pq priorityQueue) Len() int            { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool  { return pq[i].priority > pq[j].priority }
func (pq priorityQueue) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i]; pq[i].index = i; pq[j].index = j }

func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*poolJob)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

type Pool struct {
	numWorkers int
	queue      priorityQueue
	mu         sync.Mutex
	notEmpty   sync.Cond

	activeWorkers atomic.Int64
	completedJobs atomic.Int64
	failedJobs    atomic.Int64
	enqueuedJobs  atomic.Int64

	updateJobFn   func(id string, fn func(*model.Job))
	afterTerminal func(*model.Job)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(numWorkers int) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	wp := &Pool{
		numWorkers: numWorkers,
		queue:      make(priorityQueue, 0),
		ctx:        ctx,
		cancel:     cancel,
	}
	wp.notEmpty = *sync.NewCond(&wp.mu)
	for i := 0; i < numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	return wp
}

func (wp *Pool) SetJobUpdater(fn func(id string, upd func(*model.Job))) { wp.updateJobFn = fn }
func (wp *Pool) SetAfterTerminal(fn func(*model.Job))                   { wp.afterTerminal = fn }

func (wp *Pool) applyJobUpdate(pj *poolJob, upd func(*model.Job)) {
	if wp.updateJobFn != nil {
		wp.updateJobFn(pj.job.ID, upd)
	} else {
		upd(pj.job)
	}
}

func (wp *Pool) finishTerminal(pj *poolJob) {
	if wp.afterTerminal != nil {
		wp.afterTerminal(pj.job)
	}
}

func (wp *Pool) worker(id int) {
	defer wp.wg.Done()
	for {
		wp.mu.Lock()
		for wp.queue.Len() == 0 {
			select {
			case <-wp.ctx.Done():
				wp.mu.Unlock()
				return
			default:
			}
			wp.notEmpty.Wait()
		}
		pj := heap.Pop(&wp.queue).(*poolJob)
		wp.mu.Unlock()

		wp.activeWorkers.Add(1)
		func() {
			defer wp.activeWorkers.Add(-1)
			var panicked bool
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					log.Printf("[Pool] worker %d job %s panic: %v", id, pj.job.ID, r)
					wp.applyJobUpdate(pj, func(j *model.Job) {
						j.Status = model.JobStatusFailed
						j.Error = "internal error: panic during execution"
						j.UpdatedAt = time.Now()
					})
					wp.failedJobs.Add(1)
					wp.finishTerminal(pj)
				}
			}()

			wp.applyJobUpdate(pj, func(j *model.Job) {
				j.Status = model.JobStatusProcessing
				j.UpdatedAt = time.Now()
			})

			result, err := pj.fn(pj.job)
			if panicked {
				return
			}
			if err != nil {
				wp.applyJobUpdate(pj, func(j *model.Job) {
					j.Status = model.JobStatusFailed
					j.Error = err.Error()
					j.UpdatedAt = time.Now()
				})
				wp.failedJobs.Add(1)
			} else {
				wp.applyJobUpdate(pj, func(j *model.Job) {
					j.Status = model.JobStatusCompleted
					j.Result = result
					j.Progress = 100
					j.UpdatedAt = time.Now()
				})
				wp.completedJobs.Add(1)
			}
			wp.finishTerminal(pj)
		}()
	}
}

func (wp *Pool) Enqueue(ctx context.Context, job *model.Job, priority int, fn func(*model.Job) (any, error)) error {
	pj := &poolJob{job: job, fn: fn, priority: priority}
	wp.mu.Lock()
	select {
	case <-wp.ctx.Done():
		wp.mu.Unlock()
		return context.Canceled
	case <-ctx.Done():
		wp.mu.Unlock()
		return ctx.Err()
	default:
	}
	heap.Push(&wp.queue, pj)
	wp.enqueuedJobs.Add(1)
	wp.mu.Unlock()
	wp.notEmpty.Signal()
	return nil
}

func (wp *Pool) Stats() map[string]interface{} {
	wp.mu.Lock()
	qlen := wp.queue.Len()
	wp.mu.Unlock()
	return map[string]interface{}{
		"num_workers":    wp.numWorkers,
		"active_workers": wp.activeWorkers.Load(),
		"queue_length":   qlen,
		"enqueued":       wp.enqueuedJobs.Load(),
		"completed":      wp.completedJobs.Load(),
		"failed":         wp.failedJobs.Load(),
	}
}

func (wp *Pool) Shutdown() {
	wp.cancel()
	wp.notEmpty.Broadcast()
	wp.wg.Wait()
}
