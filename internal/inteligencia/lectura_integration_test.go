//go:build integration

package inteligencia

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sembrarTransferencia(t *testing.T, pool *pgxpool.Pool, desde, hacia string, monto int64,
	momento time.Time) uuid.UUID {
	t.Helper()
	transferencia := uuid.New()
	sembrarAsiento(t, pool, "retencion", &transferencia, "", momento, []lineaDePrueba{
		{"pasivo:usuario:" + desde + ":disponible", -monto},
		{"pasivo:usuario:" + desde + ":retenido", monto},
	})
	sembrarAsiento(t, pool, "despacho_riel", &transferencia, "", momento.Add(time.Minute), []lineaDePrueba{
		{"pasivo:usuario:" + desde + ":retenido", -monto},
		{"pasivo:liquidacion_pendiente:riel", monto},
	})
	sembrarAsiento(t, pool, "liquidacion_confirmada", &transferencia, "", momento.Add(2*time.Minute),
		[]lineaDePrueba{
			{"pasivo:liquidacion_pendiente:riel", -monto},
			{"pasivo:usuario:" + hacia + ":disponible", monto},
		})
	return transferencia
}

func sembrarPagoExterno(t *testing.T, pool *pgxpool.Pool, usuario, descripcion string, monto int64,
	momento time.Time) int64 {
	t.Helper()
	return sembrarAsiento(t, pool, "pago_qr", nil, descripcion, momento, []lineaDePrueba{
		{"pasivo:usuario:" + usuario + ":disponible", -monto},
		{"pasivo:liquidacion_pendiente:riel", monto},
	})
}

func TestPaginaDeMovimientosPaginaPorCursorYNoCruzaUsuarios(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	pagador, receptor := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, pagador)
	sembrarCuentaDeUsuario(t, pool, receptor)
	sembrarPersona(t, pool, receptor, "Persona Receptora")
	insertarMemoria(t, pool, pagador, "persona", "llave", receptor, CategoriaCompras)

	base := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	sembrarTransferencia(t, pool, pagador, receptor, 111, base)
	sembrarTransferencia(t, pool, pagador, receptor, 222, base.AddDate(0, 0, 1))
	sembrarTransferencia(t, pool, pagador, receptor, 333, base.AddDate(0, 0, 2))
	sembrarPagoExterno(t, pool, pagador, "Tienda Sintética", 444, base.AddDate(0, 0, 3))

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	primera, err := servicio.Movimientos(ctx, pagador, nil, 3)
	if err != nil {
		t.Fatalf("Movimientos: %v", err)
	}
	if len(primera.Movimientos) != 3 || !primera.HayMas || primera.Siguiente == nil {
		t.Fatalf("primera página = %d movimientos, hay más %v, siguiente %v",
			len(primera.Movimientos), primera.HayMas, primera.Siguiente)
	}
	if primera.Movimientos[0].MontoCentavos != 444 || primera.Movimientos[0].Categoria != string(SinCategoria) {
		t.Errorf("el más nuevo = %+v, want el pago externo sin categoría", primera.Movimientos[0])
	}
	if primera.Movimientos[1].MontoCentavos != 333 || primera.Movimientos[1].Categoria != string(CategoriaCompras) {
		t.Errorf("segundo = %+v, want la transferencia de compras", primera.Movimientos[1])
	}
	if !primera.Movimientos[0].OcurridoEn.After(primera.Movimientos[1].OcurridoEn) {
		t.Errorf("el orden no es del más nuevo al más viejo: %v", primera.Movimientos)
	}
	if primera.Movimientos[1].Contraparte != "Persona Receptora" || !primera.Movimientos[1].ContraparteResuelta {
		t.Errorf("contraparte = %+v", primera.Movimientos[1])
	}
	if primera.Movimientos[0].Contraparte != "Tienda Sintética" || primera.Movimientos[0].ContraparteResuelta {
		t.Errorf("contraparte externa = %+v", primera.Movimientos[0])
	}

	ancla, err := DecodificarCursor(*primera.Siguiente)
	if err != nil {
		t.Fatalf("DecodificarCursor: %v", err)
	}
	segunda, err := servicio.Movimientos(ctx, pagador, &ancla, 3)
	if err != nil {
		t.Fatalf("segunda página: %v", err)
	}
	if len(segunda.Movimientos) != 1 || segunda.HayMas || segunda.Siguiente != nil {
		t.Fatalf("segunda página = %d movimientos, hay más %v", len(segunda.Movimientos), segunda.HayMas)
	}
	if segunda.Movimientos[0].MontoCentavos != 111 {
		t.Errorf("último = %+v, want 111", segunda.Movimientos[0])
	}

	receptorPagina, err := servicio.Movimientos(ctx, receptor, nil, 10)
	if err != nil {
		t.Fatalf("Movimientos del receptor: %v", err)
	}
	if len(receptorPagina.Movimientos) != 3 {
		t.Fatalf("el receptor ve %d movimientos, want 3", len(receptorPagina.Movimientos))
	}
	for _, movimiento := range receptorPagina.Movimientos {
		if movimiento.Direccion != "entrada" || movimiento.MontoCentavos == 444 {
			t.Errorf("movimiento ajeno en la página del receptor: %+v", movimiento)
		}
	}
}

func TestResumenDelMesConMedianaMesesCompletosYAlcance(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario, contraparte, corto := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, cuenta := range []string{usuario, contraparte, corto} {
		sembrarCuentaDeUsuario(t, pool, cuenta)
	}
	sembrarPersona(t, pool, contraparte, "Persona Contraparte")
	insertarMemoria(t, pool, usuario, "persona", "llave", contraparte, CategoriaCompras)
	insertarMemoria(t, pool, corto, "persona", "llave", contraparte, CategoriaCompras)

	sembrarTransferencia(t, pool, usuario, contraparte, 100000, time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC))
	sembrarTransferencia(t, pool, usuario, contraparte, 200000, time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC))
	sembrarTransferencia(t, pool, usuario, contraparte, 400000, time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC))
	sembrarTransferencia(t, pool, usuario, contraparte, 500000, time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC))
	sembrarPagoExterno(t, pool, usuario, "Tienda Sintética", 70000, time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC))

	sembrarTransferencia(t, pool, corto, contraparte, 300000, time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC))
	sembrarTransferencia(t, pool, corto, contraparte, 100000, time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC))

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	resumen, err := servicio.Resumen(ctx, usuario, "2026-09")
	if err != nil {
		t.Fatalf("Resumen: %v", err)
	}
	if resumen.Mes != "2026-09" || resumen.Alcance != AlcanceBilletera || resumen.Moneda != MonedaDeLaBilletera {
		t.Errorf("cabecera = %+v", resumen)
	}
	if !resumen.Evaluable || resumen.MesesCompletos != 3 || resumen.MesesRequeridos != 3 {
		t.Errorf("evaluable = %v, meses completos = %d", resumen.Evaluable, resumen.MesesCompletos)
	}
	if resumen.TotalCentavos != 570000 {
		t.Errorf("total = %d, want 570000", resumen.TotalCentavos)
	}
	if resumen.CupoAvisos != CupoMensualDeAvisos || resumen.AvisosEmitidos != 0 {
		t.Errorf("cupo = %d, emitidos = %d", resumen.CupoAvisos, resumen.AvisosEmitidos)
	}

	compras := categoriaDelResumen(t, resumen, string(CategoriaCompras))
	if compras.TotalCentavos != 500000 || compras.MesesConGasto != 3 {
		t.Errorf("compras = %+v", compras)
	}
	if compras.MedianaCentavos == nil || *compras.MedianaCentavos != 200000 {
		t.Errorf("mediana de compras = %v, want 200000", compras.MedianaCentavos)
	}

	sinCategoria := categoriaDelResumen(t, resumen, string(SinCategoria))
	if sinCategoria.TotalCentavos != 70000 || sinCategoria.MedianaCentavos != nil {
		t.Errorf("sin categoría = %+v, want total 70000 sin mediana", sinCategoria)
	}

	cortoResumen, err := servicio.Resumen(ctx, corto, "2026-09")
	if err != nil {
		t.Fatalf("Resumen del usuario con dos meses: %v", err)
	}
	if cortoResumen.Evaluable || cortoResumen.MesesCompletos != 2 {
		t.Errorf("evaluable = %v, meses completos = %d, want no evaluable con 2",
			cortoResumen.Evaluable, cortoResumen.MesesCompletos)
	}
	cortoCompras := categoriaDelResumen(t, cortoResumen, string(CategoriaCompras))
	if cortoCompras.MedianaCentavos != nil {
		t.Errorf("mediana con un mes con gasto = %v, want nil", cortoCompras.MedianaCentavos)
	}

	vacio, err := servicio.Resumen(ctx, uuid.NewString(), "2026-09")
	if err != nil {
		t.Fatalf("Resumen sin movimientos: %v", err)
	}
	if vacio.Evaluable || vacio.MesesCompletos != 0 || len(vacio.Categorias) != 0 || vacio.TotalCentavos != 0 {
		t.Errorf("resumen vacío = %+v", vacio)
	}
}

func TestResumenRespetaLaFronteraDeMesEnBogota(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	sembrarPagoExterno(t, pool, usuario, "Tienda de Frontera", 11100,
		time.Date(2026, time.September, 30, 23, 59, 59, 0, zonaBogota))
	sembrarPagoExterno(t, pool, usuario, "Tienda de Octubre", 22200,
		time.Date(2026, time.October, 1, 0, 0, 0, 0, zonaBogota))

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	septiembre, err := servicio.Resumen(ctx, usuario, "2026-09")
	if err != nil {
		t.Fatalf("Resumen de septiembre: %v", err)
	}
	if septiembre.TotalCentavos != 11100 {
		t.Errorf("septiembre = %d, want 11100", septiembre.TotalCentavos)
	}
	octubre, err := servicio.Resumen(ctx, usuario, "2026-10")
	if err != nil {
		t.Fatalf("Resumen de octubre: %v", err)
	}
	if octubre.TotalCentavos != 22200 {
		t.Errorf("octubre = %d, want 22200", octubre.TotalCentavos)
	}
}

func categoriaDelResumen(t *testing.T, resumen ResumenDelMes, codigo string) ResumenDeCategoria {
	t.Helper()
	for _, categoria := range resumen.Categorias {
		if categoria.Codigo == codigo {
			return categoria
		}
	}
	t.Fatalf("la categoría %q no está en el resumen: %+v", codigo, resumen.Categorias)
	return ResumenDeCategoria{}
}
