# Pinned currency inputs and generated pack

`currency-policy.json` defines the reviewed catalog and hashes the unmodified
CLDR 48.0.0 files in `cldr-48/`. Its Unicode license covers these files and generated
locale projections. Run `python3 tool/currencies.py` from the repository root to
regenerate the `currencies/` pack and embedded Go metadata, without network access.
Do not hand-edit generated files. See [the guide](../docs/currencies.md) for
export, locale additions, contract coordination and migrations.
