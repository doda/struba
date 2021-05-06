CREATE TABLE kafka_phrases_stream (
    Created DateTime,
    Count UInt64,
    Phrase String
) ENGINE = Kafka(
    'kafka:9092',
    'phrases-json',
    'ch-phrases-group',
    'JSONEachRow'
);

CREATE TABLE kafka_phrases AS kafka_phrases_stream ENGINE = MergeTree() PARTITION BY toYYYYMM(Created)
ORDER BY
    Created;

CREATE MATERIALIZED VIEW kafka_phrases_consumer TO kafka_phrases AS
SELECT
    *
FROM
    kafka_phrases_stream;

CREATE MATERIALIZED VIEW kafka_phrases_1m ENGINE = SummingMergeTree() PARTITION BY toYYYYMM(Created)
ORDER BY
    (Created, Phrase) POPULATE AS
SELECT
    toStartOfMinute(Created) as Created,
    Phrase,
    sum(Count) as Count
FROM
    kafka_phrases
GROUP BY
    Created,
    Phrase;
