package main

import (
	"context"
	"errors"
	"testing"
)

// catalogosFalsos responde lo que la prueba le diga y recuerda lo que se le preguntó.
type catalogosFalsos struct {
	version      int64
	versionFinal int64
	esquema      bool
	rol          bool
	escribe      bool
	enCurso      int
	tablas       []string
	huella       HuellaDelLedger
	huellaFinal  *HuellaDelLedger
	lecturas     int
	huellas      int
}

func (c *catalogosFalsos) Version(context.Context) (int64, error) {
	c.lecturas++
	if c.lecturas > 1 && c.versionFinal != 0 {
		return c.versionFinal, nil
	}
	return c.version, nil
}

func (c *catalogosFalsos) EsquemaExiste(context.Context, string) (bool, error) { return c.esquema, nil }

func (c *catalogosFalsos) RolExiste(context.Context, string) (bool, error) { return c.rol, nil }

func (c *catalogosFalsos) RolEscribeElLedger(context.Context) (bool, error) { return c.escribe, nil }

func (c *catalogosFalsos) TrabajosEnCurso(context.Context) (int, error) { return c.enCurso, nil }

func (c *catalogosFalsos) TablasPresentes(context.Context) ([]string, error) { return c.tablas, nil }

func (c *catalogosFalsos) Huella(context.Context) (HuellaDelLedger, error) {
	c.huellas++
	if c.huellaFinal != nil && c.huellas > 1 {
		return *c.huellaFinal, nil
	}
	return c.huella, nil
}

// migradorFalso recuerda la versión a la que se le pidió bajar.
type migradorFalso struct {
	llamado bool
	version int64
	err     error
}

func (m *migradorFalso) BajarA(_ context.Context, version int64) error {
	m.llamado = true
	m.version = version
	return m.err
}

const dsnDePrueba = "postgres://dueno:clave@127.0.0.1:55437/vera_semilla_m8?sslmode=disable"

func configuracionDePrueba() Configuracion {
	return Configuracion{
		DSN:             dsnDePrueba,
		Ejecutar:        true,
		Confirmacion:    ConfirmacionDeRollback,
		VersionEsperada: VersionDeM8,
	}
}

func catalogosEnEstadoDeM8() *catalogosFalsos {
	return &catalogosFalsos{
		version:      VersionDeM8,
		versionFinal: VersionAnteriorAInteligencia,
		esquema:      false,
		rol:          false,
		tablas:       nil,
		huella:       HuellaDelLedger{Cuentas: 2, Asientos: 10, Lineas: 20, Resumen: "abc"},
	}
}

// TestRollbackSinEjecutarNoBajaNada deja el comando en modo plan.
func TestRollbackSinEjecutarNoBajaNada(t *testing.T) {
	t.Parallel()
	cfg := configuracionDePrueba()
	cfg.Ejecutar = false
	cfg.Confirmacion = ""
	migrador := &migradorFalso{}

	informe, err := Ejecutar(t.Context(), cfg, catalogosEnEstadoDeM8(), migrador)
	if err != nil {
		t.Fatalf("Ejecutar: %v", err)
	}
	if migrador.llamado {
		t.Fatal("sin --ejecutar no se baja ninguna migración")
	}
	if informe.Ejecutado {
		t.Fatal("el informe no puede decir que se ejecutó")
	}
	if len(informe.Plan) == 0 {
		t.Fatal("el plan debe imprimirse")
	}
}

// TestRollbackExigeConfirmacionLiteral cierra el comando contra un accidente de tecleo.
func TestRollbackExigeConfirmacionLiteral(t *testing.T) {
	t.Parallel()
	casos := map[string]string{
		"vacía":     "",
		"otra cosa": "si",
		"casi":      "borrar-inteligencia",
	}
	for nombre, confirmacion := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			cfg := configuracionDePrueba()
			cfg.Confirmacion = confirmacion
			migrador := &migradorFalso{}
			if _, err := Ejecutar(t.Context(), cfg, catalogosEnEstadoDeM8(), migrador); err == nil {
				t.Fatal("se esperaba un error de confirmación")
			}
			if migrador.llamado {
				t.Fatal("no se baja nada sin la confirmación literal")
			}
		})
	}
}

// TestRollbackSeNiegaConVersionInesperada no revierte una migración que no es de M8.
func TestRollbackSeNiegaConVersionInesperada(t *testing.T) {
	t.Parallel()
	catalogos := catalogosEnEstadoDeM8()
	catalogos.version = 27
	migrador := &migradorFalso{}

	_, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogos, migrador)
	if !errors.Is(err, ErrVersionInesperada) {
		t.Fatalf("error = %v, quiero ErrVersionInesperada", err)
	}
	if migrador.llamado {
		t.Fatal("no se baja nada con una versión inesperada")
	}
}

// TestRollbackSeNiegaConTrabajoEnCurso evita dejar la copia a medias.
func TestRollbackSeNiegaConTrabajoEnCurso(t *testing.T) {
	t.Parallel()
	catalogos := catalogosEnEstadoDeM8()
	catalogos.enCurso = 2
	migrador := &migradorFalso{}

	_, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogos, migrador)
	if !errors.Is(err, ErrTrabajosEnCurso) {
		t.Fatalf("error = %v, quiero ErrTrabajosEnCurso", err)
	}
	if migrador.llamado {
		t.Fatal("no se baja nada con trabajos en curso")
	}
}

// TestRollbackSeNiegaSiElRolEscribeElLedger no derriba un rol mal otorgado.
func TestRollbackSeNiegaSiElRolEscribeElLedger(t *testing.T) {
	t.Parallel()
	catalogos := catalogosEnEstadoDeM8()
	catalogos.escribe = true
	migrador := &migradorFalso{}

	_, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogos, migrador)
	if !errors.Is(err, ErrRolEscribeElLedger) {
		t.Fatalf("error = %v, quiero ErrRolEscribeElLedger", err)
	}
	if migrador.llamado {
		t.Fatal("no se baja nada si el rol escribe el ledger")
	}
}

// TestRollbackSeNiegaConDSNNoDesechable nunca apunta a la base real.
func TestRollbackSeNiegaConDSNNoDesechable(t *testing.T) {
	t.Parallel()
	cfg := configuracionDePrueba()
	cfg.DSN = "postgres://dueno:clave@10.0.0.9:5432/centinela"

	_, err := Ejecutar(t.Context(), cfg, catalogosEnEstadoDeM8(), &migradorFalso{})
	if err == nil {
		t.Fatal("se esperaba un error de DSN")
	}
}

// TestRollbackFallaSiLaHuellaCambia es la comprobación central: si el ledger cambió, no hay éxito.
func TestRollbackFallaSiLaHuellaCambia(t *testing.T) {
	t.Parallel()
	catalogos := catalogosEnEstadoDeM8()
	otra := HuellaDelLedger{Cuentas: 2, Asientos: 11, Lineas: 22, Resumen: "otra"}
	catalogos.huellaFinal = &otra

	_, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogos, &migradorFalso{})
	if !errors.Is(err, ErrHuellaCambiada) {
		t.Fatalf("error = %v, quiero ErrHuellaCambiada", err)
	}
}

// TestRollbackFelizDejaLaCopieDerribada comprueba el estado final completo.
func TestRollbackFelizDejaLaCopieDerribada(t *testing.T) {
	t.Parallel()
	migrador := &migradorFalso{}

	informe, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogosEnEstadoDeM8(), migrador)
	if err != nil {
		t.Fatalf("Ejecutar: %v", err)
	}
	if !migrador.llamado || migrador.version != VersionAnteriorAInteligencia {
		t.Fatalf("migrador = %+v, quiero bajar a %d", migrador, VersionAnteriorAInteligencia)
	}
	if informe.VersionDespues != VersionAnteriorAInteligencia {
		t.Fatalf("versión después = %d", informe.VersionDespues)
	}
	if informe.HuellaAntes != informe.HuellaDespues {
		t.Fatal("la huella del ledger no puede cambiar")
	}
	if informe.EsquemaPresente || informe.RolPresente || len(informe.TablasPresentes) != 0 {
		t.Fatalf("quedaron restos: %+v", informe)
	}
}

// TestRollbackConConservarCopiaNoBaja conserva la copia y lo dice.
func TestRollbackConConservarCopiaNoBaja(t *testing.T) {
	t.Parallel()
	cfg := configuracionDePrueba()
	cfg.ConservarCopia = true
	catalogos := catalogosEnEstadoDeM8()
	catalogos.versionFinal = VersionDeM8
	catalogos.esquema = true
	catalogos.rol = true
	catalogos.tablas = TablasDeInteligencia
	migrador := &migradorFalso{}

	informe, err := Ejecutar(t.Context(), cfg, catalogos, migrador)
	if err != nil {
		t.Fatalf("Ejecutar: %v", err)
	}
	if migrador.llamado {
		t.Fatal("con --conservar-copia no se baja nada")
	}
	if !informe.CopiaConservada {
		t.Fatal("el informe debe declarar que la copia se conservó")
	}
}

// TestRollbackFallaSiQuedanRestos no reporta éxito con la copia a medio derribar.
func TestRollbackFallaSiQuedanRestos(t *testing.T) {
	t.Parallel()
	catalogos := catalogosEnEstadoDeM8()
	catalogos.tablas = []string{"aviso"}

	if _, err := Ejecutar(t.Context(), configuracionDePrueba(), catalogos, &migradorFalso{}); err == nil {
		t.Fatal("se esperaba un error por restos")
	}
}

// TestPlanDeRollbackRespetaElOrden documenta la secuencia que se imprime y se ejecuta.
func TestPlanDeRollbackRespetaElOrden(t *testing.T) {
	t.Parallel()
	plan := PlanDeRollback(false)
	if len(plan) < 6 {
		t.Fatalf("plan = %v, quiero los pasos completos", plan)
	}
	if plan[0] != "verificar que la versión aplicada es la 26" {
		t.Fatalf("primer paso = %q", plan[0])
	}
	if ultimo := plan[len(plan)-1]; ultimo != "volver a tomar la huella del ledger y compararla" {
		t.Fatalf("último paso = %q", ultimo)
	}
	if conserva := PlanDeRollback(true); len(conserva) != len(plan)-1 {
		t.Fatalf("el plan que conserva la copia tiene un paso menos: %d y %d", len(conserva), len(plan))
	}
}
