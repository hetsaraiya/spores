// Package app holds the wired application graph built at boot.
package app

import (
	"spore/router"
	"spore/startup/config"
)

// App is the shared runtime graph for the process.
type App struct {
	Config *config.Config
	Router *router.Router
}