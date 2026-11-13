package copilot

import "reflect"

// lectorAusente descarta el nil explícito y también el puntero tipado nulo, que no es igual a nil
// cuando viaja dentro de una interfaz. Las cuatro herramientas lo usan antes de llamar cualquier
// método del lector, para responder ErrHerramientaNoDisponible en vez de entrar en un método con
// receptor nulo.
func lectorAusente(lector any) bool {
	if lector == nil {
		return true
	}
	v := reflect.ValueOf(lector)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
