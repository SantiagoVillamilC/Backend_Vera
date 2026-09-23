package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// canalCorreoFalso registra el último correo que recibió y devuelve el id o el error configurados.
type canalCorreoFalso struct {
	ultimoCorreo string
	ultimoAsunto string
	ultimoCuerpo string
	id           string
	err          error
}

func (c *canalCorreoFalso) Enviar(_ context.Context, correo, asunto, cuerpo string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	c.ultimoCorreo, c.ultimoAsunto, c.ultimoCuerpo = correo, asunto, cuerpo
	return c.id, nil
}

// canalOTPFalso registra el último celular y el último texto que recibió.
type canalOTPFalso struct {
	ultimoCelular string
	ultimoTexto   string
	id            string
	err           error
}

func (c *canalOTPFalso) Enviar(_ context.Context, celular, codigo string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	c.ultimoCelular, c.ultimoTexto = celular, codigo
	return c.id, nil
}

func TestNotificadorDeAvisosEscogeElCanalPorDestino(t *testing.T) {
	t.Parallel()

	correo := &canalCorreoFalso{id: "correo-1"}
	whatsapp := &canalOTPFalso{id: "whatsapp-1"}
	notificador := NuevoNotificadorDeAvisos(correo, whatsapp)

	id, err := notificador.Enviar(t.Context(), Aviso{
		Tipo:      TipoAvisoContrasenaCambiada,
		Destino:   DestinoCorreo,
		Correo:    "aviso@centinela.test",
		Plantilla: "marcador",
	})
	if err != nil || id != "correo-1" {
		t.Fatalf("Enviar por correo = (%q, %v), quiero (%q, nil)", id, err, "correo-1")
	}
	if correo.ultimoCorreo != "aviso@centinela.test" || correo.ultimoAsunto == "" || correo.ultimoCuerpo == "" {
		t.Errorf("el canal de correo no recibió el aviso completo: %+v", correo)
	}

	id, err = notificador.Enviar(t.Context(), Aviso{
		Tipo:      TipoAvisoCambioNumero,
		Destino:   DestinoWhatsApp,
		Celular:   "+573001112233",
		Plantilla: "marcador",
	})
	if err != nil || id != "whatsapp-1" {
		t.Fatalf("Enviar por whatsapp = (%q, %v), quiero (%q, nil)", id, err, "whatsapp-1")
	}
	if whatsapp.ultimoCelular != "+573001112233" || whatsapp.ultimoTexto == "" {
		t.Errorf("el canal de whatsapp no recibió el aviso completo: %+v", whatsapp)
	}
}

func TestNotificadorDeAvisosRechazaElDestinoDesconocido(t *testing.T) {
	t.Parallel()

	notificador := NuevoNotificadorDeAvisos(&canalCorreoFalso{}, &canalOTPFalso{})
	if _, err := notificador.Enviar(t.Context(), Aviso{Tipo: TipoAvisoCambioNumero, Destino: "paloma"}); err == nil {
		t.Fatal("un destino desconocido debía fallar")
	}
}

func TestAvisoValidoExigeDireccionYPlantilla(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		aviso   Aviso
		wantErr bool
	}{
		"correo completo":      {aviso: Aviso{Tipo: TipoAvisoContrasenaCambiada, Destino: DestinoCorreo, Correo: "a@b.test", Plantilla: "marcador"}},
		"correo sin direccion": {aviso: Aviso{Tipo: TipoAvisoContrasenaCambiada, Destino: DestinoCorreo, Plantilla: "marcador"}, wantErr: true},
		"correo sin plantilla": {aviso: Aviso{Tipo: TipoAvisoContrasenaCambiada, Destino: DestinoCorreo, Correo: "a@b.test"}, wantErr: true},
		"whatsapp completo":    {aviso: Aviso{Tipo: TipoAvisoCambioNumero, Destino: DestinoWhatsApp, Celular: "+57300", Plantilla: "marcador"}},
		"whatsapp sin celular": {aviso: Aviso{Tipo: TipoAvisoCambioNumero, Destino: DestinoWhatsApp, Plantilla: "marcador"}, wantErr: true},
		"efecto sin destino":   {aviso: Aviso{Tipo: TipoAvisoAbrirSubcuentas}},
		"sin tipo":             {aviso: Aviso{Destino: DestinoCorreo, Correo: "a@b.test", Plantilla: "marcador"}, wantErr: true},
		"destino raro":         {aviso: Aviso{Tipo: TipoAvisoCambioNumero, Destino: "paloma"}, wantErr: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if err := caso.aviso.valido(); (err != nil) != caso.wantErr {
				t.Errorf("valido() = %v, wantErr %v", err, caso.wantErr)
			}
		})
	}
}

func TestElAvisoDeCambioNoLogueaElNumeroNiElCodigo(t *testing.T) {
	const nuevo = "+573001112233"
	const soporte = "soporte@sandbox.centinela.test"

	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	base := avisoBaseDeCambio(soporte, nuevo, "2026-09-19T12:00:00Z")
	correoAviso := base
	correoAviso.Destino = DestinoCorreo
	correoAviso.Correo = "dueno@centinela.test"

	cuerpo := cuerpoDeAviso(correoAviso)
	if !strings.Contains(cuerpo, "****2233") || !strings.Contains(cuerpo, soporte) {
		t.Fatalf("el aviso no lleva el numero enmascarado ni el soporte: %q", cuerpo)
	}
	if strings.Contains(cuerpo, nuevo) || strings.Contains(cuerpo, "123456") {
		t.Fatalf("el aviso filtra el numero completo o un codigo: %q", cuerpo)
	}
	notificador := NuevoNotificadorDeAvisos(&canalCorreoFalso{id: "c"}, &canalOTPFalso{id: "w"})
	if _, err := notificador.Enviar(t.Context(), correoAviso); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if strings.Contains(buf.String(), nuevo) || strings.Contains(buf.String(), "123456") {
		t.Fatalf("el log filtra el numero completo o un codigo: %q", buf.String())
	}
}

func TestUltimoErrorDeAvisoRedactaElDestino(t *testing.T) {
	t.Parallel()

	const destino = "aviso@centinela.test"
	payload := []byte(`{"destino":"correo","correo":"` + destino + `","plantilla":"marcador"}`)
	causa := errors.New("no se pudo entregar a " + destino)

	got := ultimoErrorDeAviso(causa, payload)
	if strings.Contains(got, destino) {
		t.Errorf("ultimoErrorDeAviso = %q, no debe contener el destino", got)
	}
	if !strings.Contains(got, "[redactado]") {
		t.Errorf("ultimoErrorDeAviso = %q, quiero la marca [redactado]", got)
	}
}
