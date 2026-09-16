#!/usr/bin/env python3
"""Generate and verify the offline currency pack without network access."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + '\n').encode()


def digest(value):
    return hashlib.sha256(value).hexdigest()


def build():
    policy = json.loads((ROOT / 'reference/currency-policy.json').read_text())
    sources = ROOT / 'reference/cldr-48'
    for name, metadata in policy['sources'].items():
        if digest((sources / name).read_bytes()) != metadata['sha256']:
            raise ValueError(f'CLDR source changed: {name}')
    read = lambda name: json.loads((sources / f'{name}.json').read_text())
    units = policy['currencies']
    fractions = read('currencyData')['supplemental']['currencyData']['fractions']
    for code, digits in units.items():
        if len(code) != 3 or not code.isascii() or not code.isupper():
            raise ValueError(f'Invalid currency: {code}')
        if digits != int(fractions.get(code, fractions['DEFAULT'])['_digits']):
            raise ValueError(f'Unreviewed precision change: {code}')
    files = {}
    files['catalog.json'] = encode({
        'schema_version': 1, 'cldr_version': policy['cldr_version'],
        'currencies': {c: {'minor_units': n} for c, n in units.items()},
    })
    locales = {}
    for locale, source_locale in policy['locales'].items():
        source = read(source_locale)['main'][source_locale]['numbers']['currencies']
        items = {}
        for code in units:
            value = source[code]
            item = {'display_name': value['displayName'], 'symbol': value.get('symbol', code)}
            if not item['display_name'] or not item['symbol']:
                raise ValueError(f'Missing label: {locale}/{code}')
            if 'symbol-alt-narrow' in value:
                item['narrow_symbol'] = value['symbol-alt-narrow']
            plurals = {k.removeprefix('displayName-count-'): v for k, v in value.items() if k.startswith('displayName-count-')}
            if plurals:
                if not set(plurals) <= {'zero', 'one', 'two', 'few', 'many', 'other'}:
                    raise ValueError(f'Invalid plural category: {locale}/{code}')
                item['display_names_by_plural'] = plurals
            items[code] = item
        path = f'locales/{locale}.json'
        files[path] = encode({'schema_version': 1, 'locale': locale, 'currencies': items})
        locales[locale] = {'path': path, 'source_locale': source_locale}
    aliases = read('aliases')['supplemental']['metadata']['alias']
    files['locale_rules.json'] = encode({
        'schema_version': 1,
        'language_aliases': {k: v['_replacement'] for k, v in aliases['languageAlias'].items()},
        'territory_aliases': {k: v['_replacement'].split()[0] for k, v in aliases['territoryAlias'].items()},
        'likely_subtags': read('likelySubtags')['supplemental']['likelySubtags'],
        'parent_locales': read('parentLocales')['supplemental']['parentLocales']['parentLocale'],
    })
    files['UNICODE-LICENSE.txt'] = (sources / 'UNICODE-LICENSE.txt').read_bytes()
    hashes = {name: digest(data) for name, data in files.items()}
    files['manifest.json'] = encode({
        'schema_version': 1, 'cldr_version': policy['cldr_version'],
        'default_locale': policy['default_locale'],
        'legacy_locale_aliases': policy['legacy_locale_aliases'],
        'catalog': 'catalog.json', 'rules': 'locale_rules.json', 'locales': locales,
        'files': hashes, 'content_sha256': digest(encode(hashes)),
        'sources': policy['sources'],
    })
    metadata = encode([{'code': c, 'minor_units': units[c]} for c in sorted(units)])
    return files, metadata


def synchronize(destination, files, check):
    for name, data in files.items():
        path = destination / name
        if check:
            if not path.is_file() or path.read_bytes() != data:
                raise ValueError(f'Generated currency data differs: {path}')
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
    extras = {str(p.relative_to(destination)) for p in destination.rglob('*') if p.is_file()} - set(files)
    if extras:
        raise ValueError(f'Unexpected currency pack files: {sorted(extras)}')


def app_marker(manifest):
    """Record what an exported client pack was generated from.

    The application keeps this beside its currency assets and fails its own
    check when the record no longer matches the pack, so a backend that changed
    without re-exporting cannot leave the client silently stale.
    """
    contract = json.loads((ROOT / 'internal/apicontract/v1/openapi.json').read_text())
    return encode({
        'schema_version': 1,
        'contract_revision': contract['info']['version'],
        'cldr_version': manifest['cldr_version'],
        'currency_pack_sha256': manifest['content_sha256'],
    })


def synchronize_app(app, files, marker, check):
    synchronize(app / 'assets/reference/currencies', files, check)
    path = app / 'assets/reference/contract.json'
    if check:
        if not path.is_file():
            raise ValueError(f'The application is missing its contract record: {path}')
        if path.read_bytes() != marker:
            raise ValueError(
                'The application contract record is stale; re-export with --app '
                f'and commit the result: {path}'
            )
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(marker)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--app', type=Path, help='Explicit Flutter checkout to receive/check generated assets')
    args = parser.parse_args()
    files, metadata = build()
    synchronize(ROOT / 'reference/currencies', files, args.check)
    target = ROOT / 'internal/accounting/currencies.json'
    if args.check:
        if target.read_bytes() != metadata:
            raise ValueError('Go currency metadata differs from the canonical catalog')
    else:
        target.write_bytes(metadata)
    if args.app:
        manifest = json.loads(files['manifest.json'])
        synchronize_app(args.app, files, app_marker(manifest), args.check)
    print(f'Currency pack {"verified" if args.check else "generated"}: {len(json.loads(metadata))} currencies')


if __name__ == '__main__':
    main()
