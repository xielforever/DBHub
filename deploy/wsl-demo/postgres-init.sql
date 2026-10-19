\set ON_ERROR_STOP on
\getenv reader_password DEMO_READER_PASSWORD
\getenv writer_password DEMO_WRITER_PASSWORD

REVOKE ALL ON DATABASE dbhub_demo FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

CREATE TABLE public.inventory (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku text NOT NULL UNIQUE,
    name text NOT NULL,
    quantity integer NOT NULL CHECK (quantity >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO public.inventory (sku, name, quantity) VALUES
    ('KB-001', '键盘', 20), ('MS-001', '鼠标', 50), ('MN-001', '显示器', 12);

CREATE ROLE demo_reader LOGIN PASSWORD :'reader_password';
CREATE ROLE demo_writer LOGIN PASSWORD :'writer_password';
GRANT CONNECT ON DATABASE dbhub_demo TO demo_reader, demo_writer;
GRANT USAGE ON SCHEMA public TO demo_reader, demo_writer;
GRANT SELECT ON TABLE public.inventory TO demo_reader;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.inventory TO demo_writer;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO demo_writer;
