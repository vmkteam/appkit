//nolint:sloglint,noctx
package main

import (
	"log/slog"
	_ "net/http/pprof"

	"github.com/vmkteam/appkit"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vmkteam/zenrpc/v2"
)

const appName = "apisrv"

func main() {
	version := appkit.Version()
	slog.Info("app started", "app", appName, "version", version, "addr", "http://localhost:8080")

	// create echo server
	e := appkit.NewEcho()
	e.Use(appkit.HTTPMetrics(appName))

	md := registerMetadata()

	e.Any("/debug/pprof/*", appkit.PprofHandler)
	e.GET("/debug/metadata", md.Handler)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	e.GET("/v1/rpc/doc/", appkit.EchoHandlerFunc(zenrpc.SMDBoxHandler))
	e.GET("/", appkit.RenderRoutes(appName, e))

	// register metadata as metrics
	md.RegisterMetrics()

	// HTTP headers for client requests
	httpHeaders := appkit.NewInternalHeaders(appName, version)
	_ = httpHeaders

	// start echo
	if err := e.Start(":8080"); err != nil {
		slog.Error("http server exited", "err", err.Error())
	}
}

// registerMetadata creates metadata manager with service metadata.
func registerMetadata() *appkit.MetadataManager {
	return appkit.NewMetadataManager(appkit.MetadataOpts{
		DBs: []appkit.DBMetadata{
			appkit.NewDBMetadata("master", 5, false),
			appkit.NewDBMetadata("replica", 1, true),
		},
		HasPublicAPI:      true,
		HasPrivateAPI:     false,
		HasBrokersrvQueue: false,
		HasCronJobs:       false,
		Services: []appkit.ServiceMetadata{
			appkit.NewServiceMetadata("yandex", appkit.MetadataServiceTypeExternal),
			appkit.NewServiceMetadata("apisrv", appkit.MetadataServiceTypeAsync),
		},
	})
}
