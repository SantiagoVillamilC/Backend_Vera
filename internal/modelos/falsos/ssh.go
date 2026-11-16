package falsos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SSH es un par de scripts ssh y scp falsos. Registran lo que reciben y simulan fallos según el modo.
//
// Modos: "ok" (por defecto), "huella" (todo falla con Host key verification failed), "rechaza:N"
// (los primeros N intentos de comprobación fallan con Connection refused), "fallo_script" (el
// arranque remoto sale con 7), "tunel_muere:N" (los primeros N túneles salen enseguida).
type SSH struct {
	Dir    string
	BinSSH string
	BinSCP string
}

const scriptSSH = `#!/bin/sh
dir="%DIR%"
herramienta="%HERRAMIENTA%"
echo "$herramienta $*" >> "$dir/argv.log"
env | sort > "$dir/env.log"
modo=$(cat "$dir/modo" 2>/dev/null || echo ok)
if [ "$modo" = "huella" ]; then
  echo "Host key verification failed." >&2
  exit 255
fi
if [ "$herramienta" = "scp" ]; then
  echo "$*" >> "$dir/scp.log"
  exit 0
fi
ultimo=""
for a; do ultimo="$a"; done
case " $* " in
*" -N "*)
  echo "tunel" >> "$dir/tunel.log"
  n=$(wc -l < "$dir/tunel.log")
  case "$modo" in tunel_muere:*) [ "$n" -le "${modo#tunel_muere:}" ] && { echo "conexion perdida" >&2; exit 255; } ;; esac
  exec sleep 3600 ;;
esac
case "$ultimo" in
true)
  echo x >> "$dir/comprobar.log"
  n=$(wc -l < "$dir/comprobar.log")
  case "$modo" in rechaza:*) [ "$n" -le "${modo#rechaza:}" ] && { echo "ssh: connect to host: Connection refused" >&2; exit 255; } ;; esac
  exit 0 ;;
"bash -s")
  cat > "$dir/stdin.log"
  echo "salida de la VM con rpa_CLAVE_DE_PRUEBA_0123456789" >&2
  [ "$modo" = "fallo_script" ] && exit 7
  exit 0 ;;
esac
exit 0
`

// NuevoSSH crea los scripts en un directorio temporal de la prueba.
func NuevoSSH(t *testing.T) *SSH {
	t.Helper()
	dir := t.TempDir()
	s := &SSH{Dir: dir, BinSSH: filepath.Join(dir, "ssh"), BinSCP: filepath.Join(dir, "scp")}
	for _, h := range []struct{ ruta, nombre string }{{s.BinSSH, "ssh"}, {s.BinSCP, "scp"}} {
		contenido := strings.NewReplacer("%DIR%", dir, "%HERRAMIENTA%", h.nombre).Replace(scriptSSH)
		if err := os.WriteFile(h.ruta, []byte(contenido), 0o700); err != nil { //nolint:gosec // script de prueba ejecutable
			t.Fatal(err)
		}
	}
	return s
}

// Modo cambia el comportamiento del ssh falso.
func (s *SSH) Modo(modo string) {
	if err := os.WriteFile(filepath.Join(s.Dir, "modo"), []byte(modo), 0o600); err != nil {
		panic(err)
	}
}

func (s *SSH) leer(nombre string) string {
	datos, _ := os.ReadFile(filepath.Join(s.Dir, nombre)) //nolint:gosec // archivo de prueba dentro de un directorio temporal
	return string(datos)
}

// Argv devuelve una línea por invocación de ssh o scp.
func (s *SSH) Argv() string { return s.leer("argv.log") }

// Env devuelve el entorno de la última invocación.
func (s *SSH) Env() string { return s.leer("env.log") }

// Stdin devuelve lo que recibió el último "bash -s".
func (s *SSH) Stdin() string { return s.leer("stdin.log") }

// Tuneles cuenta cuántas veces se lanzó un túnel.
func (s *SSH) Tuneles() int { return strings.Count(s.leer("tunel.log"), "tunel") }

// Comprobaciones cuenta las invocaciones de "ssh ... true".
func (s *SSH) Comprobaciones() int { return strings.Count(s.leer("comprobar.log"), "x") }
