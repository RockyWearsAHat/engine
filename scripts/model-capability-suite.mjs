#!/usr/bin/env node

import process from 'node:process';

const DEFAULT_ENDPOINT = process.env.CAPABILITY_ENDPOINT || process.env.OLLAMA_BASE_URL || 'http://127.0.0.1:11434';
const DEFAULT_MODEL = process.env.CAPABILITY_MODEL || process.env.ENGINE_MODEL || 'qwen2.5:1.5b';
const DEFAULT_PROVIDER = process.env.CAPABILITY_PROVIDER || 'ollama';

function parseArgs(argv) {
  const opts = {
    endpoint: DEFAULT_ENDPOINT,
    model: DEFAULT_MODEL,
    provider: DEFAULT_PROVIDER,
    apiKey: process.env.OPENAI_API_KEY || '',
    timeoutMs: Number(process.env.CAPABILITY_TIMEOUT_MS || 30000),
    plannerModel: process.env.CAPABILITY_PLANNER_MODEL || '',
    workerModel: process.env.CAPABILITY_WORKER_MODEL || '',
    reviewerModel: process.env.CAPABILITY_REVIEWER_MODEL || '',
    strict: false,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--endpoint' && argv[i + 1]) {
      opts.endpoint = argv[++i];
      continue;
    }
    if (arg === '--model' && argv[i + 1]) {
      opts.model = argv[++i];
      continue;
    }
    if (arg === '--provider' && argv[i + 1]) {
      opts.provider = argv[++i];
      continue;
    }
    if (arg === '--api-key' && argv[i + 1]) {
      opts.apiKey = argv[++i];
      continue;
    }
    if (arg === '--timeout-ms' && argv[i + 1]) {
      const parsed = Number(argv[++i]);
      if (Number.isFinite(parsed) && parsed > 0) {
        opts.timeoutMs = parsed;
      }
      continue;
    }
    if (arg === '--planner-model' && argv[i + 1]) {
      opts.plannerModel = argv[++i];
      continue;
    }
    if (arg === '--worker-model' && argv[i + 1]) {
      opts.workerModel = argv[++i];
      continue;
    }
    if (arg === '--reviewer-model' && argv[i + 1]) {
      opts.reviewerModel = argv[++i];
      continue;
    }
    if (arg === '--strict') {
      opts.strict = true;
      continue;
    }
  }

  return opts;
}

function chatCompletionsUrl(endpoint) {
  return `${endpoint.replace(/\/$/, '')}/v1/chat/completions`;
}

function normalizeContent(content) {
  if (typeof content === 'string') {
    return content;
  }
  if (Array.isArray(content)) {
    return content
      .map(item => {
        if (!item || typeof item !== 'object') {
          return '';
        }
        if (typeof item.text === 'string') {
          return item.text;
        }
        if (typeof item.content === 'string') {
          return item.content;
        }
        return '';
      })
      .join('\n')
      .trim();
  }
  return '';
}

async function invokeChat({ endpoint, model, apiKey, messages, tools, timeoutMs = 30000 }) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(chatCompletionsUrl(endpoint), {
      method: 'POST',
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
      },
      body: JSON.stringify({
        model,
        stream: false,
        messages,
        ...(tools ? { tools } : {}),
      }),
    });

    if (!response.ok) {
      const body = await response.text();
      throw new Error(`chat request failed (${response.status}): ${body}`);
    }

    const payload = await response.json();
    const choice = payload?.choices?.[0];
    if (!choice || !choice.message) {
      throw new Error('chat response missing choices[0].message');
    }

    return {
      finishReason: choice.finish_reason || '',
      message: choice.message,
      usage: payload?.usage || null,
      raw: payload,
    };
  } finally {
    clearTimeout(timer);
  }
}

function safeJsonParse(input) {
  try {
    return { ok: true, value: JSON.parse(input) };
  } catch (error) {
    return { ok: false, error: String(error) };
  }
}

function stripMarkdownCodeFence(input) {
  const trimmed = String(input || '').trim();
  const match = trimmed.match(/^```(?:json)?\s*([\s\S]*?)\s*```$/i);
  if (match && match[1]) {
    return match[1].trim();
  }
  return trimmed;
}

function parseJsonLenient(input) {
  const stripped = stripMarkdownCodeFence(input);
  const direct = safeJsonParse(stripped);
  if (direct.ok) {
    return direct;
  }

  const start = stripped.indexOf('{');
  const end = stripped.lastIndexOf('}');
  if (start !== -1 && end !== -1 && end > start) {
    return safeJsonParse(stripped.slice(start, end + 1));
  }

  return direct;
}

function printHeader(title) {
  console.log('');
  console.log(`=== ${title} ===`);
}

async function testPlanning(opts) {
  const messages = [
    { role: 'system', content: 'You are concise and return JSON when asked.' },
    {
      role: 'user',
      content:
        'Return ONLY JSON object: {"goal":"string","steps":["step1","step2","step3"]}. Goal: build 2d platformer shooter with dash and gunplay.',
    },
  ];

  const result = await invokeChat({ ...opts, messages });
  const text = normalizeContent(result.message.content);
  const parsed = parseJsonLenient(text);

  let pass = false;
  let reason = '';
  if (!parsed.ok) {
    reason = `non-json response: ${parsed.error}`;
  } else if (!Array.isArray(parsed.value?.steps)) {
    reason = 'json missing steps array';
  } else if (parsed.value.steps.length < 3) {
    reason = `steps too short (${parsed.value.steps.length})`;
  } else {
    pass = true;
  }

  return {
    id: 'planning_json',
    pass,
    reason,
    evidence: text.slice(0, 400),
  };
}

function toolDefs() {
  return [
    {
      type: 'function',
      function: {
        name: 'list_files',
        description: 'List files in a workspace path.',
        parameters: {
          type: 'object',
          properties: {
            path: { type: 'string' },
          },
          required: ['path'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'write_file',
        description: 'Write text content to a file path.',
        parameters: {
          type: 'object',
          properties: {
            path: { type: 'string' },
            content: { type: 'string' },
          },
          required: ['path', 'content'],
        },
      },
    },
  ];
}

async function testToolCallSingleTurn(opts) {
  const tools = toolDefs();
  const messages = [
    {
      role: 'user',
      content:
        'Call list_files first with path "/workspace". After tool result, summarize in one sentence.',
    },
  ];

  const first = await invokeChat({ ...opts, messages, tools });
  const toolCalls = Array.isArray(first.message.tool_calls) ? first.message.tool_calls : [];
  const pass = toolCalls.length > 0 && toolCalls.some(tc => tc?.function?.name === 'list_files');

  return {
    id: 'tool_call_single_turn',
    pass,
    reason: pass ? '' : `no list_files tool call; finish_reason=${first.finishReason || 'none'}`,
    evidence: JSON.stringify(
      {
        finishReason: first.finishReason,
        toolCalls: toolCalls.map(tc => ({ id: tc.id, name: tc?.function?.name, args: tc?.function?.arguments })),
        text: normalizeContent(first.message.content),
      },
      null,
      2,
    ),
  };
}

async function testToolCallRoundTrip(opts) {
  const tools = toolDefs();
  const messages = [
    {
      role: 'user',
      content:
        'Use list_files on /workspace and then tell me one filename you found. You must use tool output.',
    },
  ];

  const first = await invokeChat({ ...opts, messages, tools });
  const toolCalls = Array.isArray(first.message.tool_calls) ? first.message.tool_calls : [];
  if (toolCalls.length === 0) {
    return {
      id: 'tool_call_round_trip',
      pass: false,
      reason: 'model did not emit any tool_calls in turn 1',
      evidence: normalizeContent(first.message.content).slice(0, 400),
    };
  }

  const call = toolCalls[0];
  const callId = call.id || 'tool-call-0';

  const followup = [
    ...messages,
    {
      role: 'assistant',
      content: normalizeContent(first.message.content) || null,
      tool_calls: toolCalls,
    },
    {
      role: 'tool',
      tool_call_id: callId,
      content: JSON.stringify({ files: ['README.md', 'src/main.ts', 'package.json'] }),
    },
  ];

  const second = await invokeChat({ ...opts, messages: followup, tools });
  const text = normalizeContent(second.message.content);
  const pass = /README\.md|src\/main\.ts|package\.json/i.test(text);

  return {
    id: 'tool_call_round_trip',
    pass,
    reason: pass ? '' : 'final answer did not use provided tool output',
    evidence: text.slice(0, 400),
  };
}

async function testReviewerVerdict(opts) {
  const messages = [
    { role: 'system', content: 'You are a strict reviewer.' },
    {
      role: 'user',
      content:
        'Given this diff summary: "Added role model overrides and tests" return exactly one word: APPROVE or REJECT.',
    },
  ];

  const result = await invokeChat({ ...opts, messages });
  const text = normalizeContent(result.message.content).trim();
  const pass = /\b(APPROVE|REJECT)\b/i.test(text);

  return {
    id: 'reviewer_verdict',
    pass,
    reason: pass ? '' : 'missing APPROVE/REJECT verdict token',
    evidence: text.slice(0, 400),
  };
}

async function runSuite(opts) {
  const results = [];

  printHeader('Model Capability Suite');
  const plannerModel = opts.plannerModel || opts.model;
  const workerModel = opts.workerModel || opts.model;
  const reviewerModel = opts.reviewerModel || opts.model;

  console.log(`provider: ${opts.provider}`);
  console.log(`endpoint: ${opts.endpoint}`);
  console.log(`default model: ${opts.model}`);
  console.log(`planner model: ${plannerModel}`);
  console.log(`worker model: ${workerModel}`);
  console.log(`reviewer model: ${reviewerModel}`);
  console.log(`timeoutMs: ${opts.timeoutMs}`);

  const plannerOpts = { ...opts, model: plannerModel };
  const workerOpts = { ...opts, model: workerModel };
  const reviewerOpts = { ...opts, model: reviewerModel };

  const tests = [
    { fn: testPlanning, opts: plannerOpts },
    { fn: testToolCallSingleTurn, opts: workerOpts },
    { fn: testToolCallRoundTrip, opts: workerOpts },
    { fn: testReviewerVerdict, opts: reviewerOpts },
  ];

  for (const test of tests) {
    const started = Date.now();
    try {
      const result = await test.fn(test.opts);
      const durationMs = Date.now() - started;
      results.push({ ...result, durationMs, model: test.opts.model });
      console.log(`${result.pass ? 'PASS' : 'FAIL'} ${result.id} (${durationMs}ms) [model=${test.opts.model}]`);
      if (!result.pass) {
        console.log(`  reason: ${result.reason}`);
      }
    } catch (error) {
      const durationMs = Date.now() - started;
      results.push({
        id: test.fn.name,
        pass: false,
        reason: String(error),
        evidence: '',
        durationMs,
        model: test.opts.model,
      });
      console.log(`FAIL ${test.fn.name} (${durationMs}ms) [model=${test.opts.model}]`);
      console.log(`  reason: ${String(error)}`);
    }
  }

  const passed = results.filter(r => r.pass).length;
  const failed = results.length - passed;
  const score = `${passed}/${results.length}`;

  printHeader('Summary');
  console.log(`score: ${score}`);
  console.log(`failed: ${failed}`);

  if (failed === 0) {
    console.log('classification: model appears tool-capable for basic planner + tool loop tasks.');
  } else if (passed >= 1) {
    console.log('classification: partial capability. model can do some tasks but tool reliability is weak.');
  } else {
    console.log('classification: likely model-side limitation (or endpoint does not support tool calls).');
  }

  printHeader('JSON Report');
  console.log(JSON.stringify({
    runAt: new Date().toISOString(),
    provider: opts.provider,
    endpoint: opts.endpoint,
    model: opts.model,
    plannerModel,
    workerModel,
    reviewerModel,
    passed,
    failed,
    total: results.length,
    results,
  }, null, 2));

  if (opts.strict && failed > 0) {
    process.exitCode = 1;
  }
}

const options = parseArgs(process.argv.slice(2));
runSuite(options).catch(error => {
  console.error('fatal:', String(error));
  process.exitCode = 1;
});
