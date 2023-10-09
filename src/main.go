package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"cloud.google.com/go/logging"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer
var meter metric.Meter
var logger *logging.Logger
var resource *sdkresource.Resource
var initResourcesOnce sync.Once
var histogram metric.Float64Histogram

func initResource() *sdkresource.Resource {
	initResourcesOnce.Do(func() {
		extraResources, _ := sdkresource.New(
			context.Background(),
			sdkresource.WithOS(),
			sdkresource.WithProcess(),
			sdkresource.WithContainer(),
			sdkresource.WithHost(),
		)
		resource, _ = sdkresource.Merge(
			sdkresource.Default(),
			extraResources,
		)
	})
	return resource
}

func initTracerProvider() *sdktrace.TracerProvider {
	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithInsecure())
	if err != nil {
		log.Fatalf("new otlp trace http exporter failed: %v", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(initResource()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}

func initMeterProvider() *sdkmetric.MeterProvider {
	ctx := context.Background()

	exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithInsecure())
	if err != nil {
		log.Fatalf("new otlp metric grpc exporter failed: %v", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(initResource()),
	)
	otel.SetMeterProvider(mp)
	return mp
}

func initLoggingClient() *logging.Logger {
	ctx := context.Background()

	projectID := os.Getenv("PROJECT_ID")
	logName := "uuid-generator"
	// Creates a client.
	c, err := logging.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	l := c.Logger(logName)
	return l
}

func main() {
	tp := initTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()
	fmt.Print("tracer ok")
	mp := initMeterProvider()
	defer func() {
		if err := mp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down meter provider: %v", err)
		}
	}()
	fmt.Print("meter ok")
	err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("runtime ok")

	tracer = tp.Tracer("uuid-generator")
	meter = mp.Meter("uuid-generator")
	logger = initLoggingClient()

	fmt.Print("tracer created")
	histogram, err = meter.Float64Histogram(
		"uuid.duration",
		metric.WithDescription("UUID Generation duration"),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Fatal(err)
	}
	r := mux.NewRouter()
	r.HandleFunc("/", generateUUIDHandler)
	http.ListenAndServe(":8080", r)
}

func generateUUIDHandler(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "generateUUIDHandler")

	defer span.End()
	start := time.Now()

	sleepForRandomTime(ctx)
	u := generateUUID(ctx)

	elapsed := time.Since(start).Seconds()

	histogram.Record(r.Context(), elapsed)
	span.SetAttributes(
		attribute.Float64("app.duration.uuid", elapsed),
	)

	w.Write([]byte(u))
}

func sleepForRandomTime(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "sleepForRandomTime")
	defer span.End()

	n := rand.Intn(10)

	err := logger.LogSync(ctx, logging.Entry{
		Trace:   "projects/" + os.Getenv("PROJECT_ID") + "/traces/" + span.SpanContext().TraceID().String(),
		SpanID:  span.SpanContext().SpanID().String(),
		Payload: "sleeping for " + fmt.Sprintf("%d", n) + " seconds",
	})

	if err != nil {
		log.Printf("Error logging: %v", err)
	}

	time.Sleep(time.Duration(n) * time.Second)
}

func generateUUID(ctx context.Context) string {
	ctx, span := tracer.Start(ctx, "doGenerateUUID")
	defer span.End()

	u := uuid.New()

	err := logger.LogSync(ctx, logging.Entry{
		Trace:   "projects/" + os.Getenv("PROJECT_ID") + "/traces/" + span.SpanContext().TraceID().String(),
		SpanID:  span.SpanContext().SpanID().String(),
		Payload: "uuid " + u.String() + " generated",
	})

	if err != nil {
		log.Printf("Error logging: %v", err)
	}

	return u.String()
}
