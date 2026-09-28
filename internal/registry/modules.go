package registry

import (
	"github.com/samber/do/v2"

	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/modules/apikey"
	"github.com/riipandi/tango/modules/appconfig"
	"github.com/riipandi/tango/modules/auditlog"
	"github.com/riipandi/tango/modules/identity"
	"github.com/riipandi/tango/modules/notification"
)

// Area is one area the application serves, together with the services it owns.
//
// It is the whole seam between this package and a module: the composition root
// knows an area provides services and produces a module, and knows nothing
// about which services those are or what the area does with them. The wiring
// inside an area — which service its features are built from, which of them
// must be validated before the listener opens — lives in the area, not here.
//
// A consumer outside this repository embeds its own area by passing one to
// New. It needs no edit to this package, which is what makes the module half
// composable rather than merely separate.
type Area struct {
	// Name reports the area in composition reports and logs.
	Name string
	// Package registers the services the area owns. It is called while the
	// container is built, so it must only register: a service is constructed
	// when something resolves it.
	Package func(do.Injector)
	// Mount resolves what the area's features need and builds the module the
	// router mounts. An error here fails the run before the listener opens.
	Mount func(do.Injector) (kernel.Module, error)
}

// Areas are the areas this application serves, in mount order.
//
// This list is the one place an area is named, so adding one is a line here
// plus whatever that area needs of its own. Nothing about an area's internals
// appears beside it.
func Areas() []Area {
	return []Area{
		{Name: identity.ModuleName, Package: identity.Package, Mount: identity.Mount},
		// The API-key area carries its own procedures and the credential the
		// authwall validates. Its service is registered by the area, the way
		// the key set is: the authenticator resolves it through the
		// container like any other consumer.
		{Name: apikey.ModuleName, Package: apikey.Package, Mount: apikey.Mount},
		// The application-configuration area serves the deployment's own
		// settings. It mounts with the other administrative areas and
		// claims nothing the earlier areas claim.
		{Name: appconfig.ModuleName, Package: appconfig.Package, Mount: appconfig.Mount},
		// The audit-log area reads what every other area writes. It mounts
		// after identity because it serves its own paths and claims nothing
		// identity claims, so the order is descriptive rather than a
		// dependency.
		{Name: auditlog.ModuleName, Package: auditlog.Package, Mount: auditlog.Mount},
		// The notification area publishes what an administrator announces
		// and reads what an account is targeted by. It mounts last for the
		// same reason the audit log does: its paths claim nothing the
		// earlier areas claim.
		{Name: notification.ModuleName, Package: notification.Package, Mount: notification.Mount},
	}
}

// areaPackages assembles the services every area owns into one package, so the
// container is built from the areas themselves rather than from a registration
// list this package keeps in step with them by hand.
func areaPackages(areas []Area) func(do.Injector) {
	packages := make([]func(do.Injector), 0, len(areas))
	for _, area := range areas {
		if area.Package != nil {
			packages = append(packages, area.Package)
		}
	}
	return do.Package(packages...)
}

// mountAreas builds the module list the router mounts, one entry per area.
//
// Each area resolves its own dependencies here, so a configuration an area
// cannot work with fails the run before the listener opens, rather than as a
// 500 on the first request that reaches it.
func mountAreas(i do.Injector, areas []Area) ([]kernel.Module, error) {
	modules := make([]kernel.Module, 0, len(areas))
	for _, area := range areas {
		module, err := area.Mount(i)
		if err != nil {
			return nil, err
		}
		modules = append(modules, module)
	}
	return modules, nil
}
