package wecom

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/riverqueue/river"
	"time"
)

type CustomerSyncDailyArgs struct{}

func (CustomerSyncDailyArgs) Kind() string { return "wecom.customer-directory-daily.v1" }

type CustomerSyncDailyWorker struct {
	river.WorkerDefaults[CustomerSyncDailyArgs]
	Service *CustomerSyncService
}

func (w *CustomerSyncDailyWorker) Work(ctx context.Context, job *river.Job[CustomerSyncDailyArgs]) error {
	if w == nil || w.Service == nil || !w.Service.Ready() {
		return ErrSyncNotReady
	}
	now := w.Service.now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	if now.Hour() < 1 {
		return nil
	}
	key := fmt.Sprintf("wecom-daily:%x:%s", sha256.Sum256([]byte(w.Service.CorpID)), now.Format("2006-01-02"))
	_, _, err := w.Service.Create(ctx, CreateCustomerSyncRun{RunKey: key, Trigger: "daily", CorpScope: "wecom-corp:" + w.Service.CorpID})
	return err
}
func CustomerSyncDailyPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return CustomerSyncDailyArgs{}, &river.InsertOpts{Queue: CustomerSyncQueue, MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "wecom-customer-directory-daily-v1", RunOnStart: true})
}
