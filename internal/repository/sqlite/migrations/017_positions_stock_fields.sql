ALTER TABLE positions ADD COLUMN net_quantity        TEXT NOT NULL DEFAULT '0';
ALTER TABLE positions ADD COLUMN avg_cost_per_share  TEXT NOT NULL DEFAULT '0';
