package auth

import "context"

// DestinatarioPorReferenciaIA revalida el perfil actual sin resolver teléfonos antiguos.
// Sólo debe recibir un ID obtenido de una referencia autenticada por el asistente.
func (r *ResolutorDestino) DestinatarioPorReferenciaIA(ctx context.Context, id string) (DestinatarioIA, bool, error) {
	filas, err := r.BuscarDestinatariosPrevios(ctx, []string{id}, "")
	if err != nil {
		return DestinatarioIA{}, false, err
	}
	if len(filas) != 1 {
		return DestinatarioIA{}, false, nil
	}
	return filas[0], true, nil
}
