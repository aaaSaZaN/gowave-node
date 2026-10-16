#!/usr/bin/env python3
"""Convert the LMDB export stream to the portable runtime ZIP index."""
import ipaddress
import json
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1], 'w', compression=zipfile.ZIP_DEFLATED) as archive:
    seen = set()
    for line in sys.stdin:
        item = json.loads(line)
        asn = item.pop('asn')
        if not isinstance(asn, int) or not 0 < asn <= 0xffffffff or asn in seen:
            raise ValueError('Invalid or duplicate ASN')
        seen.add(asn)
        for family in ('ipv4', 'ipv6'):
            for prefix in item[family]:
                ipaddress.ip_network(prefix, strict=False)
        entry = zipfile.ZipInfo(f'{asn}.json', date_time=(1980, 1, 1, 0, 0, 0))
        entry.compress_type = zipfile.ZIP_DEFLATED
        archive.writestr(entry, json.dumps(item, separators=(',', ':')))
    if not seen:
        raise ValueError('Empty ASN database')
