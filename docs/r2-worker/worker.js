const mimeTypes = {
  html: 'text/html; charset=utf-8',
  css: 'text/css; charset=utf-8',
  js: 'text/javascript; charset=utf-8',
  json: 'application/json',
  yaml: 'application/yaml',
  yml: 'application/yaml',
  txt: 'text/plain; charset=utf-8',
  xml: 'application/xml',
  svg: 'image/svg+xml',
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  ico: 'image/x-icon',
  woff: 'font/woff',
  woff2: 'font/woff2',
  ttf: 'font/ttf',
  mp4: 'video/mp4',
  pdf: 'application/pdf',
};

function objectResponse(object, key, method, status = 200) {
  const headers = new Headers();
  object.writeHttpMetadata(headers);
  if (!headers.has('Content-Type')) {
    headers.set('Content-Type', mimeTypes[key.split('.').pop()] || 'application/octet-stream');
  }
  headers.set('ETag', object.httpEtag);
  headers.set('Last-Modified', object.uploaded.toUTCString());
  headers.set('X-Robots-Tag', 'noindex, nofollow');
  return new Response(method === 'HEAD' ? null : object.body, {status, headers});
}

export default {
  async fetch(request, env) {
    const headers = {'X-Robots-Tag': 'noindex, nofollow'};
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      return new Response('Method not allowed', {
        status: 405,
        headers: {...headers, Allow: 'GET, HEAD'},
      });
    }

    const url = new URL(request.url);
    let path;
    try {
      path = decodeURIComponent(url.pathname);
    } catch {
      return new Response(request.method === 'HEAD' ? null : 'Bad request', {status: 400, headers});
    }
    const bucket = env.DOCS_BUCKET;
    const readObject = (key) => request.method === 'HEAD' ? bucket.head(key) : bucket.get(key);
    let key = path.slice(1);
    if (path.endsWith('/')) key += 'index.html';
    let object = await readObject(key);
    if (object) return objectResponse(object, key, request.method);

    if (!path.endsWith('/')) {
      key = `${path.slice(1)}.html`;
      object = await readObject(key);
      if (object) return objectResponse(object, key, request.method);

      key = `${path.slice(1)}/index.html`;
      if (await bucket.head(key)) {
        url.pathname += '/';
        return new Response(null, {status: 308, headers: {...headers, Location: url.href}});
      }
    } else if (path !== '/' && await bucket.head(`${path.slice(1, -1)}.html`)) {
      url.pathname = url.pathname.slice(0, -1);
      return new Response(null, {status: 308, headers: {...headers, Location: url.href}});
    }

    object = await readObject('404.html');
    if (object) return objectResponse(object, '404.html', request.method, 404);
    return new Response(request.method === 'HEAD' ? null : 'Not found', {
      status: 404,
      headers: {...headers, 'Content-Type': 'text/plain; charset=utf-8'},
    });
  },
};
