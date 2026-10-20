package inteligencia

import "strings"

// Canal es el origen de un pago. La rama de la cascada depende de él.
type Canal string

const (
	CanalLlave Canal = "llave"
	CanalQR    Canal = "qr"
)

// TipoDestino distingue un comercio de una persona.
type TipoDestino string

const (
	DestinoComercio TipoDestino = "comercio"
	DestinoPersona  TipoDestino = "persona"
)

// Los escalones de la cascada, en el orden en que se evalúan.
const (
	EscalonMemoria      = 1
	EscalonInterna      = 2
	EscalonMCC          = 3
	EscalonCatalogo     = 4
	EscalonLexico       = 5
	EscalonClasificador = 6
	EscalonSinCategoria = 7
	EscalonLLM          = EscalonClasificador
)

// Escalones es el conjunto de escalones activos.
type Escalones map[int]struct{}

// Contiene informa si el escalón está activo.
func (e Escalones) Contiene(escalon int) bool {
	_, activo := e[escalon]
	return activo
}

// PrimerCorte es el conjunto conservador: solo memoria privada y contraparte interna.
func PrimerCorte() Escalones {
	return Escalones{EscalonMemoria: {}, EscalonInterna: {}}
}

// PrimerCorteConClasificador enciende el escalón 6 sobre el primer corte y deja apagados MCC,
// catálogo y léxico, para que el cambio de despliegue sea mínimo y reversible.
func PrimerCorteConClasificador() Escalones {
	return Escalones{EscalonMemoria: {}, EscalonInterna: {}, EscalonClasificador: {}}
}

// TodosLosEscalones activa la cascada completa, incluido el clasificador local o el LLM.
func TodosLosEscalones() Escalones {
	return Escalones{
		EscalonMemoria: {}, EscalonInterna: {}, EscalonMCC: {},
		EscalonCatalogo: {}, EscalonLexico: {}, EscalonClasificador: {},
	}
}

// ParLexico es una palabra del léxico de la cascada y su categoría. La comparación es exacta.
type ParLexico struct {
	Palabra   string
	Categoria Categoria
}

// Entrada es lo que la cascada puede mirar de un pago. No tiene monto ni identidad del usuario.
type Entrada struct {
	Canal            Canal
	Llave            string
	Nombre           string
	MCC              string
	TipoDestino      TipoDestino
	CategoriaInterna string
	MID              string
}

// Los orígenes de una fila, los mismos códigos que admite el CHECK de la migración 0023.
const (
	OrigenMemoria      = "memoria"
	OrigenInterna      = "contraparte_interna"
	OrigenMCC          = "mcc"
	OrigenCatalogo     = "catalogo"
	OrigenLexico       = "lexico"
	OrigenTFIDF        = "tfidf"
	OrigenLLM          = "llm"
	OrigenSinCategoria = "sin_categoria"
)

// Recursos son las tablas que la cascada consulta.
type Recursos struct {
	Memoria         map[string]Categoria
	CatalogoLlaves  map[string]Categoria
	CatalogoNombres map[string]Categoria
	Lexico          []ParLexico
	CacheLLM        map[string]Categoria
	Clasificador    *Modelo
}

// Decision es el resultado de la cascada. La categoría vacía significa que ningún escalón decidió.
type Decision struct {
	Categoria    Categoria
	Escalon      int
	PendienteLLM bool
	Origen       string
}

// Decidir recorre la cascada en el orden del canal y devuelve la primera categoría válida.
func Decidir(entrada Entrada, recursos Recursos, escalones Escalones) Decision {
	nombre := ""
	if entrada.Nombre != "" {
		nombre = NormalizarNombre(entrada.Nombre)
	}

	if escalones.Contiene(EscalonMemoria) {
		claves := []string{entrada.Llave}
		if entrada.TipoDestino == DestinoComercio {
			claves = append(claves, entrada.MID, nombre)
		}
		for _, clave := range claves {
			if categoria, encontrada := buscarCategoria(recursos.Memoria, clave); encontrada {
				return Decision{Categoria: categoria, Escalon: EscalonMemoria, Origen: OrigenMemoria}
			}
		}
	}

	if escalones.Contiene(EscalonInterna) {
		if categoria := categoriaVisible(entrada.CategoriaInterna); categoria != "" {
			return Decision{Categoria: categoria, Escalon: EscalonInterna, Origen: OrigenInterna}
		}
	}

	soloLlave := entrada.Canal != CanalQR || entrada.TipoDestino != DestinoComercio
	if soloLlave {
		if escalones.Contiene(EscalonCatalogo) && entrada.TipoDestino != DestinoPersona {
			if categoria, encontrada := buscarCategoria(recursos.CatalogoLlaves, entrada.Llave); encontrada {
				return Decision{Categoria: categoria, Escalon: EscalonCatalogo, Origen: OrigenCatalogo}
			}
		}
		return Decision{Escalon: EscalonSinCategoria, Origen: OrigenSinCategoria}
	}

	if escalones.Contiene(EscalonMCC) {
		if categoria, resuelta := CategoriaPorMCC(entrada.MCC); resuelta {
			return Decision{Categoria: categoria, Escalon: EscalonMCC, Origen: OrigenMCC}
		}
	}

	if escalones.Contiene(EscalonCatalogo) {
		if categoria, encontrada := buscarCategoria(recursos.CatalogoLlaves, entrada.Llave); encontrada {
			return Decision{Categoria: categoria, Escalon: EscalonCatalogo, Origen: OrigenCatalogo}
		}
		if categoria, encontrada := buscarCategoria(recursos.CatalogoNombres, nombre); encontrada {
			return Decision{Categoria: categoria, Escalon: EscalonCatalogo, Origen: OrigenCatalogo}
		}
	}

	if escalones.Contiene(EscalonLexico) {
		if categoria, decidida := porLexico(recursos.Lexico, entrada.Nombre); decidida {
			return Decision{Categoria: categoria, Escalon: EscalonLexico, Origen: OrigenLexico}
		}
	}

	if !escalones.Contiene(EscalonClasificador) || entrada.TipoDestino != DestinoComercio || nombre == "" {
		return Decision{Escalon: EscalonSinCategoria, Origen: OrigenSinCategoria}
	}
	if categoria, encontrada := buscarCategoria(recursos.CacheLLM, nombre+"|"+entrada.Llave); encontrada {
		return Decision{Categoria: categoria, Escalon: EscalonClasificador, Origen: OrigenLLM}
	}
	if recursos.Clasificador != nil {
		if categoria, decidida := Predecir(recursos.Clasificador, nombre, mccCategoriaDe(entrada.MCC)); decidida {
			if EsCategoriaVisible(categoria) {
				return Decision{Categoria: categoria, Escalon: EscalonClasificador, Origen: OrigenTFIDF}
			}
		}
	}
	return Decision{Escalon: EscalonClasificador, PendienteLLM: true}
}

// mccCategoriaDe traduce el código de MCC a la categoría que el modelo conoce como token.
func mccCategoriaDe(mcc string) string {
	categoria, resuelta := CategoriaPorMCC(mcc)
	if !resuelta {
		return ""
	}
	return string(categoria)
}

func categoriaVisible(codigo string) Categoria {
	categoria := Categoria(codigo)
	if !EsCategoriaVisible(categoria) {
		return ""
	}
	return categoria
}

func buscarCategoria(tabla map[string]Categoria, clave string) (Categoria, bool) {
	if clave == "" {
		return "", false
	}
	categoria, existe := tabla[clave]
	if !existe || !EsCategoriaVisible(categoria) {
		return "", false
	}
	return categoria, true
}

func porLexico(lexico []ParLexico, nombre string) (Categoria, bool) {
	if nombre == "" {
		return "", false
	}
	palabras := make(map[string]struct{})
	for _, palabra := range strings.Split(NormalizarNombre(nombre), "-") {
		if palabra != "" {
			palabras[palabra] = struct{}{}
		}
	}
	halladas := make(map[Categoria]struct{})
	for _, par := range lexico {
		if _, presente := palabras[par.Palabra]; !presente {
			continue
		}
		if EsCategoriaVisible(par.Categoria) {
			halladas[par.Categoria] = struct{}{}
		}
	}
	if len(halladas) != 1 {
		return "", false
	}
	for categoria := range halladas {
		return categoria, true
	}
	return "", false
}
