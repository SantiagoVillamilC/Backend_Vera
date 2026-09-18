// Package api es la capa HTTP compartida: envelope, binding, validación y los tipos generados de api/openapi.yaml, y openapi.gen.go se regenera con go generate bajo un gate local (no hay CI en el repo).
//
//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen.yaml ../../../api/openapi.yaml
package api
