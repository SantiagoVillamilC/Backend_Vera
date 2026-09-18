// Package api es la capa HTTP compartida: el envelope de respuesta, el binding y la
// validación de cuerpos, y los tipos generados desde api/openapi.yaml.
//
// El archivo openapi.gen.go no se edita a mano: se regenera con go generate y CI verifica
// que el diff quede limpio. Las operaciones marcadas con la etiqueta public en el contrato
// no se generan, porque se registran a mano en el grupo público del router.
//
//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen.yaml ../../../api/openapi.yaml
package api
