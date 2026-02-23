package server

import (
	"context"
	"log"
	"opentela/internal/common"
	"os"
	"sync"

	"github.com/axiomhq/axiom-go/axiom"
	axiotel "github.com/axiomhq/axiom-go/axiom/otel"
)

var (
	dataset    = os.Getenv("AXIOM_DATASET")
	tracerOnce sync.Once
	tracker    *axiom.Client = nil
)

func initTracer() {
	tracerOnce.Do(func() {
		if dataset == "" {
			tracker = nil
			common.Logger.Info("AXIOM_DATASET not set, tracing disabled")
		} else {
			ctx := context.Background()
			stop, err := axiotel.InitTracing(ctx, dataset, "research-computer-coordinator", "v1.0.0")
			if err != nil {
				log.Fatal(err)
			}
			defer func() {
				if stopErr := stop(); stopErr != nil {
					log.Fatal(stopErr)
				}
			}()
			tracker, err = axiom.NewClient()
			if err != nil {
				log.Fatal(err)
			}
		}
	})
}

func IngestEvents(events []axiom.Event) {
	if tracker != nil {
		go func() {
			// expand events to axiom.Event
			res, err := tracker.IngestEvents(context.Background(), dataset, events)
			if err != nil {
				common.Logger.Error(err)
			}
			for _, fail := range res.Failures {
				common.Logger.Error(fail)
			}
		}()
	}
}
