package main

// Registers the (package-less, shared-scope) egress schema for the traefik
// dynamic-config (.build/<box>/traefik-routes.yml) charly generates.
func init() { registerCueKind("traefik_routes", "#TraefikRoutes") }
