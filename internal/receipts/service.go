package receipts

import (
	"context"
	"errors"
)

// ErrSinBackend indica que el servicio no tiene extractor configurado, el handler lo traduce a 501.
var ErrSinBackend = errors.New("receipts: no hay backend de OCR configurado")

// Extractor extrae los datos de un recibo a partir de su imagen.
type Extractor interface {
	Extract(context.Context, []byte) (Receipt, error)
}

// Service procesa recibos con el extractor configurado.
type Service struct{ client Extractor }

// NewService construye el servicio, un cliente nil deja la ruta respondiendo 501.
func NewService(client Extractor) *Service { return &Service{client: client} }

// Process delega en el extractor o devuelve ErrSinBackend si no hay ninguno.
func (s *Service) Process(ctx context.Context, image []byte) (Receipt, error) {
	if s.client == nil {
		return Receipt{}, ErrSinBackend
	}
	return s.client.Extract(ctx, image)
}
