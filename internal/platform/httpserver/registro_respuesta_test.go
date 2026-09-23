package httpserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/google/uuid"
)

func TestRespuestaDeRegistroEmpresaLlenaEmpresaIdSinTocarElId(t *testing.T) {
	t.Parallel()

	empresaID, representanteID := uuid.New(), uuid.New()
	respuesta := respuestaDeRegistroEmpresa(auth.RegistroEmpresa{
		EmpresaParteID:       empresaID,
		RepresentanteParteID: representanteID,
		RazonSocial:          "Centinela SAS",
		NIT:                  "900123456-1",
		Correo:               "empresa@centinela.test",
		Celular:              "+573001234567",
		Estado:               auth.EstadoPendienteVerificacion,
		FechaCreacion:        time.Now().UTC(),
		Desafio:              auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoRegistro},
	})

	if uuid.UUID(respuesta.Id) != representanteID {
		t.Errorf("id = %s, quiero el parte_id del representante %s", respuesta.Id, representanteID)
	}
	if respuesta.EmpresaId == nil || uuid.UUID(*respuesta.EmpresaId) != empresaID {
		t.Fatalf("empresa_id = %v, quiero el parte_id de la empresa %s", respuesta.EmpresaId, empresaID)
	}
}

func TestRespuestaDeRegistroDePersonaDejaAusenteEmpresaId(t *testing.T) {
	t.Parallel()

	respuesta := respuestaDeRegistro(auth.Registro{
		ParteID:        uuid.New(),
		Correo:         "ana@centinela.test",
		Celular:        "+573001234567",
		NombreCompleto: "Ana Centinela",
		Estado:         auth.EstadoPendienteVerificacion,
		FechaCreacion:  time.Now().UTC(),
		Desafio:        auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoRegistro},
	})

	crudo, err := json.Marshal(respuesta)
	if err != nil {
		t.Fatalf("serializar la respuesta: %v", err)
	}
	var campos map[string]any
	if err := json.Unmarshal(crudo, &campos); err != nil {
		t.Fatalf("decodificar la respuesta: %v", err)
	}
	if _, presente := campos["empresa_id"]; presente {
		t.Errorf("el alta de persona lleva empresa_id: %s", crudo)
	}
}
