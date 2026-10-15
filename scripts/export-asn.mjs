// Build-time only. Usage: node export-asn.mjs <lmdb module path> <database>
import { once } from 'node:events';
const { open } = await import(process.argv[2]);
const db = open({ path: process.argv[3], encoding: 'msgpack', readOnly: true });
try {
    for (const { key, value } of db.getRange()) {
        if (!Number.isSafeInteger(key) || key < 1 || key > 0xffffffff) throw new Error('Invalid ASN key');
        const entry = JSON.stringify({ asn: key, ipv4: value.ipv4, ipv6: value.ipv6 }) + '\n';
        if (!process.stdout.write(entry)) await once(process.stdout, 'drain');
    }
} finally { await db.close(); }
