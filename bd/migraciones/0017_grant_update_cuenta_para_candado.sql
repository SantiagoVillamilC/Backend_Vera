-- +goose Up

-- SELECT ... FOR UPDATE exige algún privilegio de UPDATE; se concede sobre id, la única columna que el candado lee, aunque habilita reescribir esa columna.
GRANT UPDATE (id) ON nucleo.cuenta TO rol_nucleo;

-- +goose Down
REVOKE UPDATE (id) ON nucleo.cuenta FROM rol_nucleo;
