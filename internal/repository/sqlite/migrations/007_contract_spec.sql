CREATE TABLE contract_specs (
    root_symbol TEXT PRIMARY KEY,
    spec        TEXT NOT NULL
);

INSERT INTO contract_specs (root_symbol, spec) VALUES
    ('/6E',  '1/125000'),
    ('/CL',  '1/1000'),
    ('/GC',  '1/100'),
    ('/MCL', '1/100'),
    ('/MHG', ''),
    ('/NG',  '1/10000'),
    ('/VXM', ''),
    ('/ZB',  '1/1000'),
    ('/ZC',  '1/50'),
    ('/ZN',  '1/1000');
