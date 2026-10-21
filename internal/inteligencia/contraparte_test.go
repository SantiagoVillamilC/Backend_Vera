package inteligencia

import (
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func apunte(entryID int64, cuenta string, monto int64, descripcion string) Apunte {
	return Apunte{
		EntryID:     entryID,
		OcurridoEn:  time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).Add(time.Duration(entryID) * time.Minute),
		CuentaID:    cuenta,
		Monto:       monto,
		Moneda:      "COP",
		Descripcion: descripcion,
	}
}

func TestUsuarioDeCuenta(t *testing.T) {
	casos := []struct {
		cuenta string
		querer string
		ok     bool
	}{
		{"pasivo:usuario:abc:disponible", "abc", true},
		{"pasivo:usuario:abc:retenido", "abc", true},
		{"pasivo:usuario::disponible", "", false},
		{"pasivo:usuario:abc", "", false},
		{"activo:custodia:banco", "", false},
		{"", "", false},
	}
	for _, caso := range casos {
		usuario, ok := UsuarioDeCuenta(caso.cuenta)
		if usuario != caso.querer || ok != caso.ok {
			t.Errorf("UsuarioDeCuenta(%q) = %q, %v, want %q, %v", caso.cuenta, usuario, ok, caso.querer, caso.ok)
		}
	}
}

func TestEfectosPorUsuarioSumaYOrdena(t *testing.T) {
	apuntes := []Apunte{
		apunte(10, "pasivo:usuario:u1:disponible", -50000, "envío"),
		apunte(11, "pasivo:usuario:u1:retenido", 50000, ""),
		apunte(12, "pasivo:usuario:u1:retenido", -50000, ""),
		apunte(13, "pasivo:usuario:u2:disponible", 50000, "recibo"),
		apunte(14, "pasivo:liquidacion_pendiente:riel", 0, ""),
	}
	efectos, err := EfectosPorUsuario(apuntes)
	if err != nil {
		t.Fatalf("EfectosPorUsuario: %v", err)
	}
	if len(efectos) != 2 {
		t.Fatalf("efectos = %d, want 2", len(efectos))
	}
	if efectos[0].UsuarioID != "u1" || efectos[0].Neto != -50000 || efectos[0].EntryAncla != 10 {
		t.Errorf("efecto de u1 = %+v", efectos[0])
	}
	if efectos[1].UsuarioID != "u2" || efectos[1].Neto != 50000 {
		t.Errorf("efecto de u2 = %+v", efectos[1])
	}
}

func TestResolverContraparteEntreUsuarios(t *testing.T) {
	apuntes := []Apunte{
		apunte(1, "pasivo:usuario:u1:disponible", -50000, "envío"),
		apunte(2, "pasivo:usuario:u2:disponible", 50000, "recibo"),
	}
	contraparte := ResolverContraparte(apuntes, "u1", map[string]string{"u2": "Persona Dos"})
	if contraparte.Texto != "Persona Dos" || !contraparte.Resuelta {
		t.Errorf("contraparte = %+v", contraparte)
	}
	if contraparte.Tipo != DestinoPersona || contraparte.Llave != "u2" {
		t.Errorf("destino = %+v", contraparte)
	}
	sinNombre := ResolverContraparte(apuntes, "u1", nil)
	if sinNombre.Texto != textoUsuarioDeLaBilletera || sinNombre.Resuelta {
		t.Errorf("sin nombre = %+v", sinNombre)
	}
}

func TestResolverContraparteInterna(t *testing.T) {
	casos := []struct {
		nombre    string
		cuentas   []string
		texto     string
		categoria Categoria
		resuelta  bool
	}{
		{"GMF", []string{"pasivo:usuario:u1:disponible", cuentaGMFPorPagar}, textoGMF, CategoriaComisiones, true},
		{"comisión", []string{"pasivo:usuario:u1:disponible", cuentaComision}, textoComision, CategoriaComisiones, true},
		{"custodia", []string{"pasivo:usuario:u1:disponible", "activo:custodia:banco"}, textoBancoCustodio, "", true},
		{"liquidación", []string{"pasivo:usuario:u1:disponible", "pasivo:liquidacion_pendiente:riel"}, textoComercioExterno, "", false},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			apuntes := make([]Apunte, 0, len(caso.cuentas))
			for indice, cuenta := range caso.cuentas {
				apuntes = append(apuntes, apunte(int64(indice+1), cuenta, 1000, ""))
			}
			contraparte := ResolverContraparte(apuntes, "u1", nil)
			if contraparte.Texto != caso.texto {
				t.Errorf("texto = %q, want %q", contraparte.Texto, caso.texto)
			}
			if contraparte.CategoriaInterna != caso.categoria {
				t.Errorf("categoría interna = %q, want %q", contraparte.CategoriaInterna, caso.categoria)
			}
			if contraparte.Resuelta != caso.resuelta {
				t.Errorf("resuelta = %v, want %v", contraparte.Resuelta, caso.resuelta)
			}
		})
	}
}

func TestResolverContraparteRespaldo(t *testing.T) {
	conDescripcion := []Apunte{apunte(1, "pasivo:usuario:u1:disponible", -1000, "PAGO COMERCIO")}
	if contraparte := ResolverContraparte(conDescripcion, "u1", nil); contraparte.Texto != "PAGO COMERCIO" || contraparte.Resuelta {
		t.Errorf("con descripción = %+v", contraparte)
	}
	sinDescripcion := []Apunte{apunte(1, "pasivo:usuario:u1:disponible", -1000, "")}
	if contraparte := ResolverContraparte(sinDescripcion, "u1", nil); contraparte.Texto != textoMovimiento {
		t.Errorf("sin descripción = %+v", contraparte)
	}
}

func TestAbsolutoYSuma(t *testing.T) {
	if valor, ok := Absoluto(-50000); !ok || valor != 50000 {
		t.Errorf("Absoluto(-50000) = %d, %v", valor, ok)
	}
	if _, ok := Absoluto(-1 << 63); ok {
		t.Error("Absoluto aceptó el mínimo de int64")
	}
	if _, desborda := sumarEnteros(1<<63-1, 1); !desborda {
		t.Error("sumarEnteros no detectó el desbordamiento")
	}
}

func TestConstruirGruposUneLaTransferenciaCompleta(t *testing.T) {
	transaccion := uuid.New()
	retencion := apunte(2, "pasivo:usuario:u1:disponible", -50000, "envío")
	retencion.TransferenciaID = transaccion
	retencion.TieneTransferencia = true
	despacho := apunte(3, "pasivo:usuario:u1:retenido", -50000, "")
	despacho.TransferenciaID = transaccion
	despacho.TieneTransferencia = true
	receptor := apunte(4, "pasivo:usuario:u2:disponible", 50000, "")
	receptor.TransferenciaID = transaccion
	receptor.TieneTransferencia = true

	lineas := []repositorio.LineasDeAsientosRow{filaDeAsiento(retencion), filaDeAsiento(despacho)}
	transferencias := []repositorio.LineasDeTransferenciasRow{
		filaDeTransferencia(retencion), filaDeTransferencia(despacho), filaDeTransferencia(receptor),
	}
	grupos := construirGrupos([]int64{2, 3}, lineas, transferencias)
	if len(grupos) != 1 {
		t.Fatalf("grupos = %d, want 1", len(grupos))
	}
	if grupos[0].entryAncla != 2 || len(grupos[0].apuntes) != 3 {
		t.Errorf("grupo = %+v", grupos[0])
	}
	if !grupos[0].tieneTransferencia || grupos[0].transaccionID != transaccion {
		t.Errorf("transferencia del grupo = %+v", grupos[0])
	}
}

func TestTransaccionDeGrupoEsDeterminista(t *testing.T) {
	g := grupo{clave: "entry:42"}
	primera := transaccionDeGrupo(g)
	segunda := transaccionDeGrupo(g)
	if primera != segunda {
		t.Errorf("identificador inestable: %s y %s", primera, segunda)
	}
	if primera == uuid.Nil {
		t.Error("identificador nulo")
	}
}

func filaDeAsiento(a Apunte) repositorio.LineasDeAsientosRow {
	return repositorio.LineasDeAsientosRow{
		EntryID:         a.EntryID,
		Tipo:            a.Tipo,
		TransferenciaID: pgtype.UUID{Bytes: a.TransferenciaID, Valid: a.TieneTransferencia},
		OcurridoEn:      pgtype.Timestamptz{Time: a.OcurridoEn, Valid: true},
		Descripcion:     pgtype.Text{String: a.Descripcion, Valid: a.Descripcion != ""},
		CuentaID:        a.CuentaID,
		Monto:           a.Monto,
		Moneda:          a.Moneda,
	}
}

func filaDeTransferencia(a Apunte) repositorio.LineasDeTransferenciasRow {
	return repositorio.LineasDeTransferenciasRow(filaDeAsiento(a))
}
