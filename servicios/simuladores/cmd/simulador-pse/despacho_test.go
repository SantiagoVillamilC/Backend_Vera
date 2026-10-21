package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// enviadorFalso registra cada envío y simula la caída del destino.
type enviadorFalso struct {
	mu       sync.Mutex
	llamadas []Envio
	err      error
	fallos   int
}

// Enviar acumula el envío y devuelve un fallo mientras queden fallos por consumir.
func (f *enviadorFalso) Enviar(_ context.Context, e Envio) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.llamadas = append(f.llamadas, e)
	if f.fallos > 0 {
		f.fallos--
		return errors.New("destino caído")
	}
	return f.err
}

func (f *enviadorFalso) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.llamadas)
}

func (f *enviadorFalso) ultimo(t *testing.T) Envio {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.llamadas) == 0 {
		t.Fatal("el enviador no registró ninguna llamada")
	}
	return f.llamadas[len(f.llamadas)-1]
}

func almacenDePrueba(t *testing.T) *Almacen {
	t.Helper()
	almacen, err := AbrirAlmacen(t.TempDir() + "/custodio.db")
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })
	return almacen
}

func envioDePrueba(estado string) Envio {
	return Envio{
		Referencia: "dep-00000000-0000-4000-8000-0000000000ff",
		Monto:      150000,
		Moneda:     "COP",
		Estado:     estado,
		OcurridoEn: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
	}
}

func patasPorNombre(t *testing.T, almacen *Almacen, envioID int64) map[string]Pata {
	t.Helper()
	patas, err := almacen.PatasDe(t.Context(), envioID)
	if err != nil {
		t.Fatalf("leer patas: %v", err)
	}
	porNombre := make(map[string]Pata, len(patas))
	for _, p := range patas {
		porNombre[p.Nombre] = p
	}
	return porNombre
}

// esperarHasta repite la condición cada pocos milisegundos hasta que se cumple o vence el tope.
func esperarHasta(t *testing.T, cond func() bool) {
	t.Helper()
	limite := time.Now().Add(2 * time.Second)
	for time.Now().Before(limite) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("la condición no se cumplió antes del tope")
}

// arrancarDespacho corre el bucle de fondo hasta que la prueba termina.
func arrancarDespacho(t *testing.T, despachador *Despachador) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	listo := make(chan struct{})
	go func() {
		defer close(listo)
		despachador.Correr(ctx, 5*time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		<-listo
	})
}

// Un envío despertado se entrega en el bucle de fondo, sin que el llamador espere los POST.
func TestHU_NF_09_DespertarEntregaSinBloquearAlLlamador(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)
	arrancarDespacho(t, despachador)

	if _, err := almacen.Registrar(t.Context(), envioDePrueba(estadoAprobado), time.Now().UTC()); err != nil {
		t.Fatalf("registrar envío: %v", err)
	}
	despachador.Despertar()

	esperarHasta(t, func() bool { return nucleo.total() == 1 && custodio.total() == 1 })
}

// El archivo del almacén queda restringido al dueño, porque guarda referencia, monto y CUS.
func TestAbrirAlmacenRestringeElArchivoA0600(t *testing.T) {
	anterior := syscall.Umask(0)
	defer syscall.Umask(anterior)

	ruta := t.TempDir() + "/custodio.db"
	almacen, err := AbrirAlmacen(ruta)
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })

	info, err := os.Stat(ruta)
	if err != nil {
		t.Fatalf("stat del almacén: %v", err)
	}
	if permisos := info.Mode().Perm(); permisos != 0o600 {
		t.Errorf("permisos = %o, quiero 600", permisos)
	}
}

// La base nace en 0600 pese a un umask permisivo, antes de que SQLite escriba en ella.
func TestCrearArchivoDejaLaBaseEn0600AntesDeUsarla(t *testing.T) {
	anterior := syscall.Umask(0)
	defer syscall.Umask(anterior)

	ruta := t.TempDir() + "/custodio.db"
	if err := crearArchivo(ruta); err != nil {
		t.Fatalf("crear archivo: %v", err)
	}
	info, err := os.Stat(ruta)
	if err != nil {
		t.Fatalf("stat del almacén: %v", err)
	}
	if permisos := info.Mode().Perm(); permisos != 0o600 {
		t.Errorf("permisos = %o, quiero 600", permisos)
	}
}

// Con una transacción abierta el journal es observable y hereda el 0600 de la base.
func TestJournalHeredaEl0600DeLaBase(t *testing.T) {
	anterior := syscall.Umask(0)
	defer syscall.Umask(anterior)

	ruta := t.TempDir() + "/custodio.db"
	almacen, err := AbrirAlmacen(ruta)
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })

	tx, err := almacen.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(),
		`INSERT INTO envio (referencia, monto, moneda, estado, cus, ocurrido_en, creado_en)
		 VALUES ('ref', 1, 'COP', 'aprobado', NULL, 'x', 'y')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	info, err := os.Stat(ruta + "-journal")
	if err != nil {
		t.Fatalf("stat del journal: %v", err)
	}
	if permisos := info.Mode().Perm(); permisos != 0o600 {
		t.Errorf("journal = %o, quiero 600", permisos)
	}
}

// El helper extiende el 0600 a los sidecars preexistentes que quedaron con permisos laxos.
func TestRestringirArchivoAlcanzaLosSidecars(t *testing.T) {
	ruta := t.TempDir() + "/custodio.db"
	for _, sufijo := range []string{"-journal", "-wal", "-shm"} {
		if err := os.WriteFile(ruta+sufijo, []byte("x"), 0o644); err != nil {
			t.Fatalf("crear sidecar %s: %v", sufijo, err)
		}
	}
	restringirArchivo(ruta)
	for _, sufijo := range []string{"-journal", "-wal", "-shm"} {
		info, err := os.Stat(ruta + sufijo)
		if err != nil {
			t.Fatalf("stat del sidecar %s: %v", sufijo, err)
		}
		if permisos := info.Mode().Perm(); permisos != 0o600 {
			t.Errorf("sidecar %s = %o, quiero 600", sufijo, permisos)
		}
	}
}

// Una autorización entrega las dos patas, hacia el núcleo y hacia el simulador de custodia.
func TestHU_NF_07_Criterio15_LasDosPatasSeEntreganAlAutorizar(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)
	arrancarDespacho(t, despachador)
	envio := envioDePrueba(estadoAprobado)
	envio.CUS = "cus-simulado"

	envioID, err := almacen.Registrar(t.Context(), envio, time.Now().UTC())
	if err != nil {
		t.Fatalf("registrar envío: %v", err)
	}
	despachador.Despertar()

	esperarHasta(t, func() bool {
		patas := patasPorNombre(t, almacen, envioID)
		return patas[pataNucleo].Estado == pataEntregada && patas[pataNucleo].Entregado &&
			patas[pataCustodio].Estado == pataEntregada && patas[pataCustodio].Entregado
	})
	if nucleo.total() != 1 || custodio.total() != 1 {
		t.Fatalf("llamadas = núcleo %d, custodia %d, quiero 1 en cada una", nucleo.total(), custodio.total())
	}
	if nucleo.ultimo(t).Referencia != envio.Referencia || custodio.ultimo(t).Referencia != envio.Referencia {
		t.Error("las patas no llevaron la referencia del envío")
	}
	for nombre, p := range patasPorNombre(t, almacen, envioID) {
		if p.Estado != pataEntregada || !p.Entregado {
			t.Errorf("pata %s = %q entregado=%v, quiero entregada", nombre, p.Estado, p.Entregado)
		}
	}
}

// La caída de un destino no frena la entrega de la otra pata.
func TestHU_NF_07_Criterio15_LasDosPatasSonIndependientes(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{err: errors.New("núcleo caído")}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)

	envioID, err := almacen.Registrar(t.Context(), envioDePrueba(estadoAprobado), time.Now().UTC())
	if err != nil {
		t.Fatalf("registrar envío: %v", err)
	}
	despachador.Despachar(t.Context())

	if custodio.total() != 1 {
		t.Fatalf("la pata de custodia se entregó %d veces, quiero 1 pese a la caída del núcleo", custodio.total())
	}
	patas := patasPorNombre(t, almacen, envioID)
	if patas[pataCustodio].Estado != pataEntregada {
		t.Errorf("pata de custodia = %q, quiero entregada", patas[pataCustodio].Estado)
	}
	if patas[pataNucleo].Estado != pataPendiente || patas[pataNucleo].Intentos != 1 {
		t.Errorf("pata de núcleo = %q con %d intentos, quiero pendiente con 1",
			patas[pataNucleo].Estado, patas[pataNucleo].Intentos)
	}
}

// Un fallo empuja disponible_en y, al volver el destino, la entrega ocurre una sola vez.
func TestHU_NF_07_Criterio15_ReintentoEmpujaDisponibleYEntregaUnaSolaVez(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{fallos: 1}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)
	reloj := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	despachador.ahora = func() time.Time { return reloj }

	envioID, err := almacen.Registrar(t.Context(), envioDePrueba(estadoAprobado), reloj)
	if err != nil {
		t.Fatalf("registrar envío: %v", err)
	}

	despachador.Despachar(t.Context())
	pata := patasPorNombre(t, almacen, envioID)[pataNucleo]
	if pata.Intentos != 1 {
		t.Fatalf("intentos = %d, quiero 1", pata.Intentos)
	}
	if !pata.DisponibleEn.After(reloj) {
		t.Errorf("disponible_en = %v, quiero un instante posterior a %v", pata.DisponibleEn, reloj)
	}

	despachador.Despachar(t.Context())
	if nucleo.total() != 1 {
		t.Fatalf("reintentó antes de vencer el backoff: %d llamadas", nucleo.total())
	}

	reloj = reloj.Add(time.Hour)
	despachador.Despachar(t.Context())
	if nucleo.total() != 2 {
		t.Fatalf("llamadas tras volver el destino = %d, quiero 2", nucleo.total())
	}
	pata = patasPorNombre(t, almacen, envioID)[pataNucleo]
	if pata.Estado != pataEntregada || !pata.Entregado {
		t.Fatalf("pata = %q entregado=%v, quiero entregada", pata.Estado, pata.Entregado)
	}

	despachador.Despachar(t.Context())
	if nucleo.total() != 2 {
		t.Errorf("se entregó dos veces: %d llamadas, quiero 2", nucleo.total())
	}
}

// Agotado el tope de intentos, la pata queda en fallo terminal y no se reintenta más.
func TestHU_NF_07_Criterio15_TopeDeIntentosDejaFalloTerminal(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{err: errors.New("núcleo caído")}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)
	reloj := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	despachador.ahora = func() time.Time { return reloj }

	envioID, err := almacen.Registrar(t.Context(), envioDePrueba(estadoAprobado), reloj)
	if err != nil {
		t.Fatalf("registrar envío: %v", err)
	}
	for range maxIntentosPata + 2 {
		despachador.Despachar(t.Context())
		reloj = reloj.Add(time.Hour)
	}

	pata := patasPorNombre(t, almacen, envioID)[pataNucleo]
	if pata.Estado != pataFallida {
		t.Errorf("pata = %q, quiero fallida", pata.Estado)
	}
	if pata.Intentos != maxIntentosPata {
		t.Errorf("intentos = %d, quiero %d", pata.Intentos, maxIntentosPata)
	}
	if nucleo.total() != maxIntentosPata {
		t.Errorf("llamadas = %d, quiero exactamente %d", nucleo.total(), maxIntentosPata)
	}
}

// Un rechazo solo dispara la señal hacia el núcleo; la pata de custodia queda sin aplicar.
func TestHU_NF_07_Criterio17_RechazoNoDespachaLaPataDeCustodia(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)

	envioID, err := almacen.Registrar(t.Context(), envioDePrueba(estadoRechazado), time.Now().UTC())
	if err != nil {
		t.Fatalf("registrar envío: %v", err)
	}
	despachador.Despachar(t.Context())

	if custodio.total() != 0 {
		t.Errorf("la pata de custodia se disparó %d veces en un rechazo, quiero 0", custodio.total())
	}
	if nucleo.total() != 1 || nucleo.ultimo(t).Estado != estadoRechazado {
		t.Errorf("la señal al núcleo = %d llamadas con estado %q, quiero un rechazo",
			nucleo.total(), nucleo.ultimo(t).Estado)
	}
	if pata := patasPorNombre(t, almacen, envioID)[pataCustodio]; pata.Estado != pataNoAplica {
		t.Errorf("pata de custodia = %q, quiero %q", pata.Estado, pataNoAplica)
	}
}
