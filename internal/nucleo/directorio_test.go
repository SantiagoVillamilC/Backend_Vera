package nucleo

import (
	"context"
	"errors"
	"testing"
	"time"
)

// directorioFalso cuenta las llamadas y delega en fn, para probar que una llave inválida no consulta.
type directorioFalso struct {
	calls int
	fn    func(ctx context.Context, llave Llave) (ResolucionDirectorio, error)
}

func (d *directorioFalso) Resolver(ctx context.Context, llave Llave) (ResolucionDirectorio, error) {
	d.calls++
	return d.fn(ctx, llave)
}

// HU-NF-10, criterio 2: los cinco tipos de Bre-B validan la forma de su valor.
func TestHU_NF_10_Criterio2_ParsearLlaveAceptaLosCincoTipos(t *testing.T) {
	t.Parallel()

	casos := map[string]struct{ tipo, valor string }{
		"documento":    {tipo: TipoLlaveDocumento, valor: "1020304050"},
		"celular":      {tipo: TipoLlaveCelular, valor: "3001234567"},
		"correo":       {tipo: TipoLlaveCorreo, valor: "ana@ejemplo.com"},
		"alfanumerico": {tipo: TipoLlaveAlfanumerico, valor: "@ana2026"},
		"comercio":     {tipo: TipoLlaveComercio, valor: "TIENDA01"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			llave, err := ParsearLlave(caso.tipo, "  "+caso.valor+"  ")
			if err != nil {
				t.Fatalf("ParsearLlave(%q, %q) = %v, quiero nil", caso.tipo, caso.valor, err)
			}
			if llave.Tipo != caso.tipo || llave.Valor != caso.valor {
				t.Errorf("llave = %+v, quiero tipo %q y valor %q normalizados", llave, caso.tipo, caso.valor)
			}
		})
	}
}

// HU-NF-10, criterio 2: un tipo desconocido o un valor con otra forma se rechazan.
func TestHU_NF_10_Criterio2_ParsearLlaveRechazaTipoYFormaInvalidos(t *testing.T) {
	t.Parallel()

	casos := map[string]struct{ tipo, valor string }{
		"tipo desconocido":                {tipo: "nip", valor: "12345678"},
		"tipo vacío":                      {tipo: "", valor: "3001234567"},
		"documento con letras":            {tipo: TipoLlaveDocumento, valor: "1020ABC"},
		"documento corto":                 {tipo: TipoLlaveDocumento, valor: "12345"},
		"celular que no empieza por tres": {tipo: TipoLlaveCelular, valor: "2001234567"},
		"celular de nueve":                {tipo: TipoLlaveCelular, valor: "300123456"},
		"correo sin arroba":               {tipo: TipoLlaveCorreo, valor: "ana.ejemplo.com"},
		"correo sin dominio":              {tipo: TipoLlaveCorreo, valor: "ana@"},
		"alfanumerico sin arroba":         {tipo: TipoLlaveAlfanumerico, valor: "ana2026"},
		"alfanumerico con espacio":        {tipo: TipoLlaveAlfanumerico, valor: "@ana 2026"},
		"comercio muy corto":              {tipo: TipoLlaveComercio, valor: "TIEN"},
		"valor vacío":                     {tipo: TipoLlaveCelular, valor: "   "},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			if _, err := ParsearLlave(caso.tipo, caso.valor); !errors.Is(err, ErrLlaveInvalida) {
				t.Errorf("ParsearLlave(%q, %q) = %v, quiero ErrLlaveInvalida", caso.tipo, caso.valor, err)
			}
		})
	}
}

// HU-NF-10, criterio 2: una llave inválida se rechaza sin que el directorio reciba ninguna llamada.
func TestHU_NF_10_Criterio2_LlaveInvalidaNoConsultaElDirectorio(t *testing.T) {
	t.Parallel()

	casos := map[string]struct{ tipo, valor string }{
		"tipo desconocido":     {tipo: "nip", valor: "1020304050"},
		"documento con letras": {tipo: TipoLlaveDocumento, valor: "1020ABC"},
		"celular de nueve":     {tipo: TipoLlaveCelular, valor: "300123456"},
		"correo sin dominio":   {tipo: TipoLlaveCorreo, valor: "ana@"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			directorio := &directorioFalso{fn: func(context.Context, Llave) (ResolucionDirectorio, error) {
				t.Error("el directorio no debía ser consultado con una llave inválida")
				return ResolucionDirectorio{}, nil
			}}
			servicio := NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio))

			if _, err := servicio.ResolverLlave(t.Context(), caso.tipo, caso.valor); !errors.Is(err, ErrLlaveInvalida) {
				t.Errorf("ResolverLlave(%q, %q) = %v, quiero ErrLlaveInvalida", caso.tipo, caso.valor, err)
			}
			if directorio.calls != 0 {
				t.Errorf("el directorio recibió %d llamadas, quiero 0", directorio.calls)
			}
		})
	}
}

// HU-NF-10, criterio 4: el directorio devuelve la entidad y el producto, nunca una cuenta.
func TestHU_NF_10_Criterio4_ResolucionTraeEntidadYProducto(t *testing.T) {
	t.Parallel()

	directorio := &directorioFalso{fn: func(_ context.Context, llave Llave) (ResolucionDirectorio, error) {
		if llave != (Llave{Tipo: TipoLlaveCelular, Valor: "3001234567"}) {
			t.Errorf("llave = %+v, quiero el celular normalizado", llave)
		}
		return ResolucionDirectorio{Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"}, nil
	}}
	servicio := NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio))

	resolucion, err := servicio.ResolverLlave(t.Context(), TipoLlaveCelular, "3001234567")
	if err != nil {
		t.Fatalf("ResolverLlave: %v", err)
	}
	if resolucion.Entidad != "Banco Vecino" || resolucion.Producto != "Cuenta de ahorros" {
		t.Errorf("resolución = %+v, quiero la entidad y el producto del directorio", resolucion)
	}
	if directorio.calls != 1 {
		t.Errorf("el directorio recibió %d llamadas, quiero 1", directorio.calls)
	}
}

// HU-NF-10, criterio 5.1: una llave que el directorio no reconoce se rechaza de forma definitiva.
func TestHU_NF_10_Criterio5_1_LlaveInexistenteSeRechaza(t *testing.T) {
	t.Parallel()

	directorio := &directorioFalso{fn: func(context.Context, Llave) (ResolucionDirectorio, error) {
		return ResolucionDirectorio{}, ErrLlaveNoEncontrada
	}}
	servicio := NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio))

	_, err := servicio.ResolverLlave(t.Context(), TipoLlaveCelular, "3001234567")
	if !errors.Is(err, ErrLlaveNoEncontrada) {
		t.Fatalf("ResolverLlave = %v, quiero ErrLlaveNoEncontrada", err)
	}
	if errors.Is(err, ErrDirectorioSinRespuesta) {
		t.Errorf("una llave inexistente no es un error temporal: %v", err)
	}
}

// HU-NF-10, criterio 5.2: un directorio sin respuesta da error temporal reintentable, no un rechazo.
func TestHU_NF_10_Criterio5_2_DirectorioSinRespuestaEsTemporal(t *testing.T) {
	t.Parallel()

	t.Run("error del directorio", func(t *testing.T) {
		t.Parallel()

		directorio := &directorioFalso{fn: func(context.Context, Llave) (ResolucionDirectorio, error) {
			return ResolucionDirectorio{}, errors.New("conexión rechazada")
		}}
		servicio := NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio))

		_, err := servicio.ResolverLlave(t.Context(), TipoLlaveCelular, "3001234567")
		if !errors.Is(err, ErrDirectorioSinRespuesta) {
			t.Fatalf("ResolverLlave = %v, quiero ErrDirectorioSinRespuesta", err)
		}
		if errors.Is(err, ErrLlaveNoEncontrada) {
			t.Errorf("un fallo del directorio no es una llave inexistente: %v", err)
		}
	})

	t.Run("timeout simulado", func(t *testing.T) {
		t.Parallel()

		directorio := &directorioFalso{fn: func(ctx context.Context, _ Llave) (ResolucionDirectorio, error) {
			<-ctx.Done()
			return ResolucionDirectorio{}, ctx.Err()
		}}
		servicio := NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio))

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := servicio.ResolverLlave(ctx, TipoLlaveCelular, "3001234567")
		if !errors.Is(err, ErrDirectorioSinRespuesta) {
			t.Fatalf("ResolverLlave = %v, quiero ErrDirectorioSinRespuesta", err)
		}
		if errors.Is(err, ErrLlaveNoEncontrada) {
			t.Errorf("un timeout no es una llave inexistente: %v", err)
		}
	})
}

// HU-NF-10, criterio 4: sin directorio configurado la resolución falla cerrada.
func TestHU_NF_10_Criterio4_SinDirectorioFallaCerrado(t *testing.T) {
	t.Parallel()

	servicio := NewServicio(nil, nil, nil, nil, nil)
	if _, err := servicio.ResolverLlave(t.Context(), TipoLlaveCelular, "3001234567"); !errors.Is(err, ErrDirectorioNoDisponible) {
		t.Fatalf("ResolverLlave = %v, quiero ErrDirectorioNoDisponible", err)
	}
}
