# ECB daily model and meanings

These CC0-1.0 files contain authored metadata only. They describe the native
`time`, `currency`, `rate` strings of `ecb-daily.example.json`, separately from
ECB historical resources. The ModelSpec module is `ecb`, record
`FxReferenceQuote`; its logical model address is
`modelspec://github.com/openvaultdb/ovdb/ecb`. The MeaningGraph address is
`meaning://github.com/openvaultdb/ovdb`, with registry id `ecb-daily` and the
explicit meaning-file path in that registry. The model files are in the current
ModelSpec spelling (`1.0-draft-2`: `record`, `field`); the ECB pin chain pins them
by the bytes they had in the earlier one (`1.0-draft`: `entity`, `property`), and
the files here are exactly the rename of those bytes. The graph has no identifier binding
and the model has no stable key. `(time, currency)` is semantic grain within a
daily response; a read observation distinguishes revisions. Model fields
remain native: definition `fieldMapping` maps each native name to the same
model field. `referenceDate` and `quoteCurrency` are explanatory semantic
aliases, not renamed fields in this model or native query output.

EUR and `indicative-reference` are explicit source-definition context. There is
no EUR row or modeled base-currency property. The date is a Gregorian reference
date, without a time zone or publication instant. Decimal strings preserve all
digits and trailing zeros; the lexical pattern does not prove positivity or
calendar validity, which runtime admission must validate. The core currency
values are a starter subset, not a complete ECB coverage list.

Publication of metadata does not activate a database or grant source reuse,
retention, commercial-use or joined-output rights. The original rates are
available free from [ECB daily XML](https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml).
The source is the [European Central Bank](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html),
subject to [ECB reuse conditions](https://www.ecb.europa.eu/services/using-our-site/disclaimer/html/index.en.html).
No real rates, XML, terms text, fixtures or snapshots are copied here.

The live-source declaration stays blocked with execution disabled. Final exact
definition/model/decoder binding, runtime rights and observation evidence,
no-retention enforcement, actual browser journeys/notices and paid applicability
or an enforced free-only route are prerequisites to activation. Historical
AdventureWorks comparisons and currency-validity joins remain unresolved;
registry publication accepts neither. Existing bootstrap artifacts and all
unselected research remain retained and inactive.

Evidence: [ECB reference-rate page](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html)
and [methodology framework](https://www.ecb.europa.eu/stats/pdf/exchange/Frameworkfortheeuroforeignexchangereferencerates.en.pdf).
The framework distinguishes rate setting from publication and allows republication;
neither establishes a daily-average or closing-price meaning.
