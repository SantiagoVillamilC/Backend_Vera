# Runbook SCRUM-29: enciende centinela-vm. Sin parámetros a propósito,
# ver infra/azure/automatizar-horario.sh para el porqué.
Disable-AzContextAutosave -Scope Process
Connect-AzAccount -Identity
Start-AzVM -ResourceGroupName "centinela-rg" -Name "centinela-vm"
