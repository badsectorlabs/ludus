import assert from 'node:assert/strict';
import {after, before, test} from 'node:test';
import {fileURLToPath} from 'node:url';
import {Miniflare} from 'miniflare';

let worker;
let bucket;
before(async () => {
  worker = new Miniflare({
    modules: true,
    scriptPath: fileURLToPath(new URL('./worker.js', import.meta.url)),
    compatibilityDate: '2025-10-11',
    r2Buckets: ['DOCS_BUCKET'],
  });
  bucket = await worker.getR2Bucket('DOCS_BUCKET');
  await bucket.put('index.html', '<h1>Beta home</h1>');
  await bucket.put('docs/intro.html', '<h1>Beta intro</h1>');
  await bucket.put('api/index.html', '<script data-url="./ludus.openapi.yaml"></script>');
  await bucket.put('api/ludus.openapi.yaml', 'openapi: 3.0.3\n', {
    httpMetadata: {contentType: 'application/yaml', cacheControl: 'max-age=60'},
  });
  await bucket.put('assets/main.js', 'window.beta = true;', {
    httpMetadata: {contentType: 'text/javascript; charset=utf-8'},
  });
  await bucket.put('404.html', '<h1>Page not found</h1>');
});
after(async () => { await worker?.dispose(); });

const request = (path, options) => worker.dispatchFetch(`https://beta.ludus.cloud${path}`, options);

test('serves the root and clean docs indexes without an SPA fallback', async () => {
  const root = await request('/');
  assert.equal(root.status, 200);
  assert.equal(await root.text(), '<h1>Beta home</h1>');
  const page = await request('/docs/intro');
  assert.equal(page.status, 200);
  assert.equal(page.headers.get('Content-Type'), 'text/html; charset=utf-8');
  assert.equal(page.headers.get('X-Robots-Tag'), 'noindex, nofollow');
  assert.equal(await page.text(), '<h1>Beta intro</h1>');
  const slash = await request('/docs/intro/?source=sidebar', {redirect: 'manual'});
  assert.equal(slash.status, 308);
  assert.equal(slash.headers.get('Location'), 'https://beta.ludus.cloud/docs/intro?source=sidebar');
});

test('redirects directories, preserves queries, and resolves the relative API schema locally', async () => {
  const redirect = await request('/api?version=beta', {redirect: 'manual'});
  assert.equal(redirect.status, 308);
  assert.equal(redirect.headers.get('Location'), 'https://beta.ludus.cloud/api/?version=beta');
  const page = await request('/api/');
  const schemaPath = (await page.text()).match(/data-url="([^"]+)"/)[1];
  const schemaUrl = new URL(schemaPath, redirect.headers.get('Location'));
  assert.equal(schemaUrl.href, 'https://beta.ludus.cloud/api/ludus.openapi.yaml');
  const schema = await worker.dispatchFetch(schemaUrl);
  assert.equal(schema.status, 200);
  assert.equal(schema.headers.get('Content-Type'), 'application/yaml');
  assert.equal(schema.headers.get('Cache-Control'), 'max-age=60');
  assert.equal(await schema.text(), 'openapi: 3.0.3\n');
});

test('GET and HEAD preserve R2 metadata while HEAD never returns a body', async () => {
  const stored = await bucket.head('assets/main.js');
  const get = await request('/assets/main.js');
  const head = await request('/assets/main.js', {method: 'HEAD'});
  assert.equal(get.status, 200);
  assert.equal(head.status, 200);
  for (const response of [get, head]) {
    assert.equal(response.headers.get('ETag'), stored.httpEtag);
    assert.equal(response.headers.get('Last-Modified'), stored.uploaded.toUTCString());
    assert.equal(response.headers.get('Content-Type'), 'text/javascript; charset=utf-8');
  }
  assert.equal(await get.text(), 'window.beta = true;');
  assert.equal(await head.text(), '');
  const redirect = await request('/api', {method: 'HEAD', redirect: 'manual'});
  assert.equal(redirect.status, 308);
  assert.equal(await redirect.text(), '');
});

test('missing pages and assets return the custom 404 with a real 404 status', async () => {
  for (const path of ['/docs/missing', '/docs/missing/', '/assets/missing.js']) {
    const response = await request(path);
    assert.equal(response.status, 404);
    assert.equal(await response.text(), '<h1>Page not found</h1>');
  }
  const head = await request('/missing', {method: 'HEAD'});
  assert.equal(head.status, 404);
  assert.equal(await head.text(), '');
});

test('rejects writes and malformed URL encoding', async () => {
  const post = await request('/docs/intro/', {method: 'POST', body: 'write'});
  assert.equal(post.status, 405);
  assert.equal(post.headers.get('Allow'), 'GET, HEAD');
  assert.equal((await request('/%ZZ')).status, 400);
  const head = await request('/%ZZ', {method: 'HEAD'});
  assert.equal(head.status, 400);
  assert.equal(await head.text(), '');
});

test('returns a plain 404 when no custom page exists', async () => {
  await bucket.delete('404.html');
  const missing = await request('/absent');
  assert.equal(missing.status, 404);
  assert.equal(missing.headers.get('Content-Type'), 'text/plain; charset=utf-8');
});
