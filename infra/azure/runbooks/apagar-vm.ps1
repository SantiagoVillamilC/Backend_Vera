# Runbook SCRUM-29: apaga (deallocate) centinela-vm. Sin parámetros a
# propósito, ver infra/azure/automatizar-horario.sh para el porqué.
Disable-AzContextAutosave -Scope Process
Connect-AzAccount -Identity
# Sin -StayProvisioned: por defecto Stop-AzVM deallocata la VM (libera el
# cómputo y deja de facturarlo), que es justo el comportamiento que pide la
# tarjeta, no un shutdown del sistema operativo que siga facturando cómputo.
Stop-AzVM -ResourceGroupName "centinela-rg" -Name "centinela-vm" -Force
