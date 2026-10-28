-- Catálogo propuesto: ejecutar como dueño de migraciones después de propuesta_v2.sql.
-- No inferir que todo envío es una compra ni que toda recarga es ingreso ganado.
INSERT INTO finanzas.categoria(codigo,nombre,naturaleza,icono,color) VALUES
('alimentacion','Alimentación','gasto','restaurant','#F59E0B'),
('transporte','Transporte','gasto','directions_bus','#06B6D4'),
('vivienda','Vivienda','gasto','home','#8B5CF6'),
('servicios','Servicios y comunicaciones','gasto','bolt','#EAB308'),
('salud','Salud','gasto','health_and_safety','#EF4444'),
('educacion','Educación','gasto','school','#3B82F6'),
('compras','Compras','gasto','shopping_bag','#EC4899'),
('ocio','Ocio','gasto','movie','#A855F7'),
('impuestos_comisiones','Impuestos y comisiones','gasto','receipt','#64748B'),
('otros_gastos','Otros gastos','gasto','more_horiz','#94A3B8'),
('salario','Salario','ingreso','work','#10B981'),
('ventas','Ventas','ingreso','store','#22C55E'),
('otros_ingresos','Otros ingresos','ingreso','add_circle','#14B8A6'),
('traslado_propio','Traslado entre cuentas propias','traslado','swap_horiz','#6366F1'),
('financiacion','Préstamos y financiación recibida','ajuste','account_balance','#78716C'),
('ajuste','Ajuste sin consumo','ajuste','tune','#71717A')
ON CONFLICT (propietario_id,codigo) DO NOTHING;
