package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/viper"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const clusterContext = "cluster"
const LevelTrace slog.Level = -8

type ContextHandler struct {
	slog.Handler
}

// Handle overrides the default Handle method to add context values.
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if clusterName, ok := ctx.Value(clusterContext).(string); ok {
		r.AddAttrs(slog.String(clusterContext, clusterName))
	}
	return h.Handler.Handle(ctx, r)
}

func EnrichContext(current context.Context, clusterName string) context.Context {
	return context.WithValue(current, clusterContext, clusterName)
}

func InitLogging() {
	var level slog.Level
	levelString := viper.GetString(LogLevel)
	if strings.EqualFold(levelString, "trace") {
		level = LevelTrace
	} else {
		err := level.UnmarshalText([]byte(viper.GetString(LogLevel)))
		if err != nil {
			panic(fmt.Errorf("invalid log level %q: %w", viper.GetString(LogLevel), err))
		}
	}

	options := &slog.HandlerOptions{
		Level: level,
	}

	// create default handler: either standard text or json, if required
	var handler slog.Handler
	if viper.GetBool(LogJson) {
		handler = slog.NewJSONHandler(os.Stdout, options)
	} else {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	// wrap default handler with context value enrichment
	ctxHandler := ContextHandler{Handler: handler}

	// set default log config
	slog.SetDefault(slog.New(ctxHandler))
	// adjust controller-runtime log settings with our own config
	log.SetLogger(logr.FromSlogHandler(ctxHandler))
}
