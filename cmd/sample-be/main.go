package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const serviceName = "sample-be"

// setupOTel wires traces, metrics and logs to the OTLP endpoint configured
// through the standard OTEL_EXPORTER_OTLP_* env vars.
func setupOTel(ctx context.Context) (func(context.Context) error, error) {
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)

	metricExp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	logExp, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, err
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)), sdklog.WithResource(res))
	global.SetLoggerProvider(lp)

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}

func sleepJitter(min, max time.Duration) {
	time.Sleep(min + time.Duration(rand.Int63n(int64(max-min))))
}

// fail logs and returns an error response with the given probability.
func fail(w http.ResponseWriter, r *http.Request, p float64, msg string) bool {
	if rand.Float64() >= p {
		return false
	}
	slog.ErrorContext(r.Context(), msg, "path", r.URL.Path)
	http.Error(w, msg, http.StatusInternalServerError)
	return true
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	shutdown, err := setupOTel(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "otel setup:", err)
		os.Exit(1)
	}

	// Logs go to stdout and to Loki (via OTLP), tagged with trace ids.
	slog.SetDefault(slog.New(fanout{
		slog.NewJSONHandler(os.Stdout, nil),
		otelslog.NewHandler(serviceName),
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		sleepJitter(5*time.Millisecond, 40*time.Millisecond)
		if fail(w, r, 0.01, "failed to load users: db connection reset") {
			return
		}
		slog.InfoContext(r.Context(), "users listed")
		fmt.Fprintln(w, `{"users":[]}`)
	})
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		sleepJitter(10*time.Millisecond, 120*time.Millisecond)
		if fail(w, r, 0.03, "failed to query orders: timeout") {
			return
		}
		slog.InfoContext(r.Context(), "orders listed")
		fmt.Fprintln(w, `{"orders":[]}`)
	})
	mux.HandleFunc("/api/checkout", func(w http.ResponseWriter, r *http.Request) {
		sleepJitter(50*time.Millisecond, 400*time.Millisecond)
		if fail(w, r, 0.10, "payment gateway error: upstream returned 502") {
			return
		}
		slog.InfoContext(r.Context(), "checkout completed")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	srv := &http.Server{Addr: ":8080", Handler: otelhttp.NewHandler(mux, "http.server")}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		shutdown(sctx)
	}()

	slog.Info("sample-be listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", "err", err)
	}
}

// fanout sends each record to every handler.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(a []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(a)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
