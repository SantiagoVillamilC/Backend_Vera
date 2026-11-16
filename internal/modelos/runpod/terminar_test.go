package runpod

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func terminadorDePrueba(t *testing.T, esperas ...time.Duration) (Terminador, *[]time.Duration) {
	t.Helper()
	c, _ := nuevoCliente(t)
	dormidas := &[]time.Duration{}
	return Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: esperas,
		Dormir: func(_ context.Context, d time.Duration) error { *dormidas = append(*dormidas, d); return nil },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil))}, dormidas
}

func TestTerminarReintentaTrasFallosYVerificaConLista(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.FallosDelete = 2
	dormidas := []time.Duration{}
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second},
		Dormir: func(_ context.Context, d time.Duration) error { dormidas = append(dormidas, d); return nil },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.Terminar(t.Context(), id)
	if !r.Terminado || f.Existe(id) || f.Vivos("vera-modelos") != 0 {
		t.Fatalf("resultado %+v", r)
	}
	if len(dormidas) != 2 || dormidas[0] != 5*time.Second || dormidas[1] != 10*time.Second {
		t.Fatalf("esperas %v", dormidas)
	}
}

func TestTerminarUn204SinEfectoNoCuentaComoExito(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.DeleteSinEfecto = 1
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Second, time.Second},
		Dormir: func(context.Context, time.Duration) error { return nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.Terminar(t.Context(), id)
	if !r.Terminado || r.Intentos != 2 {
		t.Fatalf("resultado %+v", r)
	}
}

func TestTerminarTambienBorraOtrosPodsConElMismoNombreYNoTocaLosAjenos(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.AnadirPod("vera-modelos", "EXITED", time.Now())
	ajeno := f.AnadirPod("tesis-campana", "RUNNING", time.Now())
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Second},
		Dormir: func(context.Context, time.Duration) error { return nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.Terminar(t.Context(), id)
	if !r.Terminado || f.Vivos("vera-modelos") != 0 || !f.Existe(ajeno) {
		t.Fatalf("resultado %+v ajeno vivo=%v", r, f.Existe(ajeno))
	}
}

func TestTerminarAgotaIntentosYReportaRestantes(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.FallosDelete = 100
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Second, time.Second},
		Dormir: func(context.Context, time.Duration) error { return nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.Terminar(t.Context(), id)
	if r.Terminado || len(r.Restantes) != 1 || r.Restantes[0] != id || r.Motivo == "" {
		t.Fatalf("resultado %+v", r)
	}
}

func TestTerminarConListaCaidaNoDeclaraExito(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.ListarFalla = true
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Second},
		Dormir: func(context.Context, time.Duration) error { return nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.Terminar(t.Context(), id)
	if r.Terminado {
		t.Fatalf("sin poder verificar no puede ser exito: %+v", r)
	}
	if f.Existe(id) {
		t.Fatal("aun sin lista debio intentar el DELETE del id explicito")
	}
}

func TestTerminarSinPodsEsExitoInmediato(t *testing.T) {
	term, dormidas := terminadorDePrueba(t, time.Second)
	r := term.Terminar(t.Context())
	if !r.Terminado || len(*dormidas) != 0 {
		t.Fatalf("resultado %+v", r)
	}
}

func TestTerminarRespetaCancelacion(t *testing.T) {
	c, f := nuevoCliente(t)
	f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.FallosDelete = 100
	ctx, cancelar := context.WithCancel(t.Context())
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Hour},
		Dormir: func(ctx context.Context, _ time.Duration) error { cancelar(); return ctx.Err() },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil))}
	if r := term.Terminar(ctx); r.Terminado {
		t.Fatalf("resultado %+v", r)
	}
}

func TestCalendarios(t *testing.T) {
	completo := EsperasCompletas()
	if len(completo) != 23 || completo[0] != 5*time.Second || completo[6] != 120*time.Second || completo[22] != 120*time.Second {
		t.Fatalf("calendario completo %v", completo)
	}
	if len(EsperasBreves()) != 4 {
		t.Fatal("calendario breve")
	}
}

func TestTerminarSoloNoTocaOtrosPodsConElMismoNombre(t *testing.T) {
	c, f := nuevoCliente(t)
	objetivo := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	otro := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	term := Terminador{Cliente: c, Nombre: "vera-modelos", Esperas: []time.Duration{time.Second},
		Dormir: func(context.Context, time.Duration) error { return nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := term.TerminarSolo(t.Context(), objetivo)
	if !r.Terminado || f.Existe(objetivo) || !f.Existe(otro) {
		t.Fatalf("resultado %+v objetivo=%v otro=%v", r, f.Existe(objetivo), f.Existe(otro))
	}
}
