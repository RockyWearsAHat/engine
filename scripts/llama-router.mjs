#!/usr/bin/env node

import http from 'node:http';
import process from 'node:process';

function parseBackends(spec) {
  const byModel = new Map();
  const chunks = String(spec || '')
    .split(';')
    .map(s => s.trim())
    .filter(Boolean);

  for (const chunk of chunks) {
    const [rawModel, rawTargets] = chunk.split('=');
    const model = String(rawModel || '').trim();
    const targets = String(rawTargets || '')
      .split(',')
      .map(s => s.trim().replace(/\/$/, ''))
      .filter(Boolean);
    if (!model || targets.length === 0) {
      continue;
    }
    byModel.set(model, targets);
  }

  return byModel;
}

function parseDefaultBackends(spec) {
  return String(spec || '')
    .split(',')
    .map(s => s.trim().replace(/\/$/, ''))
    .filter(Boolean);
}

function nowIso() {
  return new Date().toISOString();
}

function healthPayload(modelBackends, defaultBackends) {
  const models = {};
  for (const [model, backends] of modelBackends.entries()) {
    models[model] = backends;
  }
  return {
    status: 'ok',
    timestamp: nowIso(),
    strategy: 'round-robin',
    defaultBackends,
    modelBackends: models,
  };
}

const host = process.env.LLAMA_ROUTER_HOST || '127.0.0.1';
const port = Number(process.env.LLAMA_ROUTER_PORT || '8080');
const modelBackends = parseBackends(process.env.LLAMA_ROUTER_BACKENDS || '');
const defaultBackends = parseDefaultBackends(process.env.LLAMA_ROUTER_DEFAULT_BACKENDS || '');
const modelCursors = new Map();
let defaultCursor = 0;

if (modelBackends.size === 0 && defaultBackends.length === 0) {
  console.error('[llama-router] no backends configured');
  console.error('[llama-router] set LLAMA_ROUTER_BACKENDS or LLAMA_ROUTER_DEFAULT_BACKENDS');
  process.exit(1);
}

function nextFromPool(key, backends) {
  if (!Array.isArray(backends) || backends.length === 0) {
    return '';
  }
  if (key === '__default__') {
    const index = defaultCursor % backends.length;
    defaultCursor = (defaultCursor + 1) % Number.MAX_SAFE_INTEGER;
    return backends[index];
  }

  const current = modelCursors.get(key) || 0;
  const index = current % backends.length;
  modelCursors.set(key, (current + 1) % Number.MAX_SAFE_INTEGER);
  return backends[index];
}

function chooseBackend(modelName) {
  const cleanModel = String(modelName || '').trim();
  if (cleanModel && modelBackends.has(cleanModel)) {
    const target = nextFromPool(cleanModel, modelBackends.get(cleanModel));
    return { target, pool: cleanModel };
  }

  if (defaultBackends.length > 0) {
    const target = nextFromPool('__default__', defaultBackends);
    return { target, pool: '__default__' };
  }

  if (modelBackends.size > 0) {
    const firstPool = modelBackends.keys().next().value;
    const target = nextFromPool(firstPool, modelBackends.get(firstPool));
    return { target, pool: firstPool };
  }

  return { target: '', pool: '' };
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on('data', chunk => chunks.push(chunk));
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

function parseModelFromBody(rawBody) {
  if (!rawBody || rawBody.length === 0) {
    return '';
  }
  try {
    const parsed = JSON.parse(String(rawBody));
    if (parsed && typeof parsed === 'object' && typeof parsed.model === 'string') {
      return parsed.model.trim();
    }
  } catch {
    return '';
  }
  return '';
}

const server = http.createServer(async (req, res) => {
  try {
    if ((req.url || '').startsWith('/health')) {
      const payload = healthPayload(modelBackends, defaultBackends);
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify(payload));
      return;
    }

    const body = await readBody(req);
    const model = parseModelFromBody(body);
    const { target, pool } = chooseBackend(model);

    if (!target) {
      res.writeHead(503, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: 'no backend available' }));
      return;
    }

    const url = `${target}${req.url || ''}`;
    const headers = { ...req.headers };
    delete headers.host;
    delete headers['content-length'];

    const upstream = await fetch(url, {
      method: req.method || 'GET',
      headers,
      body: body.length > 0 ? body : undefined,
    });

    const upstreamBody = Buffer.from(await upstream.arrayBuffer());
    const responseHeaders = {};
    upstream.headers.forEach((value, key) => {
      responseHeaders[key] = value;
    });
    responseHeaders['x-llama-router-upstream'] = target;
    responseHeaders['x-llama-router-pool'] = pool;

    res.writeHead(upstream.status, responseHeaders);
    res.end(upstreamBody);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    res.writeHead(502, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: 'upstream request failed', message }));
  }
});

server.listen(port, host, () => {
  const summary = {
    host,
    port,
    defaultBackends,
    modelPools: Array.from(modelBackends.entries()),
  };
  console.log(`[llama-router] listening on http://${host}:${port}`);
  console.log(`[llama-router] config ${JSON.stringify(summary)}`);
});
