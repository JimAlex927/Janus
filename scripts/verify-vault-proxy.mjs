// Anonymous checks only: never logs in, unlocks a vault, or reads stored entries.
// node scripts/verify-vault-proxy.mjs https://host:port/vault/ path/to/rootCA.crt
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import https from 'node:https';

const base = new URL(process.argv[2]);
assert.equal(base.protocol, 'https:');
assert.ok(base.pathname.endsWith('/'), 'base URL needs a trailing slash');
const agent = new https.Agent({ ca: readFileSync(process.argv[3]) });
async function request(url, method = 'GET', headers = {}) {
  return new Promise((resolve, reject) => {
    const req = https.request(url, { agent, method, headers }, response => {
      let body = '';
      response.setEncoding('utf8');
      response.on('data', chunk => { body += chunk; if (body.length > 4_000_000) response.destroy(new Error('response too large')); });
      response.on('error', reject);
      response.on('end', () => resolve({ status: response.statusCode, headers: response.headers, body }));
    });
    req.setTimeout(15000, () => req.destroy(new Error('request timeout')));
    req.on('error', reject);
    req.end();
  });
}
try {
  const page = await request(base);
  assert.equal(page.status, 200);
  const assets = [...page.body.matchAll(/(?:src|href)="([^"]+\.(?:js|css))"/g)].map(match => new URL(match[1], base));
  assert.ok(assets.length >= 2, 'no JS/CSS assets found');
  for (const url of assets) {
    assert.equal(url.origin, base.origin);
    assert.ok(url.pathname.startsWith(base.pathname), 'asset escaped mount prefix');
    const asset = await request(url);
    assert.equal(asset.status, 200);
    assert.ok(!String(asset.headers['content-type']).includes('text/html'), 'asset returned SPA HTML');
  }
  const status = await request(new URL('api/auth/status', base));
  assert.equal(status.status, 200);
  assert.equal(JSON.parse(status.body).result.workspace, false, 'maintenance port accidentally exposed');
  const redirect = await request(new URL(base.pathname.slice(0, -1), base));
  assert.equal(redirect.status, 308);
  assert.equal(redirect.headers.location, base.pathname);
  const probe = new URL('api/auth/__janus_proxy_probe__', base);
  const sameOrigin = await request(probe, 'POST', { Origin: base.origin });
  assert.equal(sameOrigin.status, 404, `same-origin POST rejected: ${sameOrigin.body}`);
  const crossOrigin = await request(probe, 'POST', { Origin: 'https://untrusted.invalid' });
  assert.equal(crossOrigin.status, 403);
  assert.ok(crossOrigin.body.includes('cross-origin request rejected'));
  for (const path of ['/janus/', '/files/', '/vault-other/']) {
    assert.equal((await request(new URL(path, base))).status, 404, `unexpected public route ${path}`);
  }
  console.log(`PASS ${base}: verified TLS, HTML, JS/CSS, Vault-only status, 308, same-origin POST, cross-origin rejection and route isolation`);
} finally { agent.destroy(); }
