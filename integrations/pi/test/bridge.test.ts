import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";
import { test } from "node:test";
import {
  fauxAssistantMessage,
  fauxProvider,
  fauxToolCall,
  type FauxResponseStep,
} from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";
import bridge from "../src/index.ts";

async function eventually(check: () => boolean) {
  const deadline = Date.now() + 5000;
  while (!check()) {
    assert.ok(
      Date.now() < deadline,
      "Pi bridge did not reach the expected state",
    );
    await delay(10);
  }
}

async function fixture(responses: FauxResponseStep[]) {
  const root = await mkdtemp(join(tmpdir(), "prunelle-pi-"));
  const requests: {
    path: string;
    method: string;
    body: any;
    author?: string;
  }[] = [];
  let stream: ServerResponse | undefined;
  const missingInvocations = new Set<string>();
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const text = Buffer.concat(chunks).toString();
    const body = text ? JSON.parse(text) : undefined;
    requests.push({
      path: request.url!,
      method: request.method!,
      body,
      author: request.headers["prunelle-agent"] as string,
    });
    if (request.url?.endsWith("/connect")) {
      stream = response;
      response.writeHead(200, { "Content-Type": "text/event-stream" });
      response.write(
        'event: ready\ndata: {"connection_id":"fixture-channel"}\n\n',
      );
      return;
    }
    response.setHeader("Content-Type", "application/json");
    const invocationID = request.url?.match(
      /^\/v1\/agent-invocations\/([^/]+)/,
    )?.[1];
    if (invocationID && missingInvocations.has(invocationID)) {
      response.statusCode = 404;
      response.end(
        JSON.stringify({ error: { message: "The review was deleted" } }),
      );
      return;
    }
    if (request.url === "/v1/agent-sessions")
      response.end(JSON.stringify({ id: "agent_fixture" }));
    else if (request.url === "/v1/discussions/dis_original")
      response.end(
        JSON.stringify({
          id: "dis_original",
          messages: [{ body: "An earlier question, retrieved by ID" }],
        }),
      );
    else if (request.url?.startsWith("/v1/revisions/rev_original/file?"))
      response.end(
        JSON.stringify({
          revision_id: "rev_original",
          repository_id: "repo_fixture",
          path: "long.ts",
          base_text: "Captured base\n",
          head_text: "x".repeat(60_000) + "\nTAIL_CAPTURED_NOT_LIVE\n",
        }),
      );
    else response.end(JSON.stringify({ id: "result_fixture" }));
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const oldServer = process.env.PRUNELLE_SERVER;
  process.env.PRUNELLE_SERVER = `http://127.0.0.1:${address.port}`;
  const model = fauxProvider({ provider: "prunelle-fixture" });
  model.setResponses(responses);
  const runtime = await ModelRuntime.create({
    authPath: join(root, "auth.json"),
    modelsPath: null,
    refreshOnCreate: false,
  });
  runtime.registerNativeProvider(model.provider);
  await runtime.setRuntimeApiKey(
    model.getModel().provider,
    "fixture-not-a-real-key",
  );
  const settings = SettingsManager.inMemory({
    retry: { enabled: false },
    compaction: { enabled: false },
  });
  const loader = new DefaultResourceLoader({
    cwd: root,
    agentDir: root,
    settingsManager: settings,
    noExtensions: true,
    noSkills: true,
    noThemes: true,
    noPromptTemplates: true,
    noContextFiles: true,
    extensionFactories: [bridge],
  });
  await loader.reload();
  const { session } = await createAgentSession({
    cwd: root,
    agentDir: root,
    model: model.getModel(),
    modelRuntime: runtime,
    settingsManager: settings,
    resourceLoader: loader,
    tools: ["prunelle_*"],
    sessionManager: SessionManager.inMemory(root),
  });
  const errors: string[] = [];
  await session.bindExtensions({
    onError: (error) => errors.push(error.error),
  });
  await eventually(
    () =>
      !!stream &&
      requests.some((r) => r.path.endsWith("/state") && r.body.ready),
  );
  return {
    session,
    model,
    requests,
    errors,
    missingInvocations,
    send(
      id: string,
      discussionID = "dis_original",
      overrides: Record<string, unknown> = {},
    ) {
      assert.ok(stream);
      const reported = requests.findLast(
        (request) => request.body?.ready === true,
      )!.body;
      stream.write(
        `event: request\ndata: ${JSON.stringify({ id, review_id: "rvw_fixture", revision_id: "rev_original", intent: "DISCUSS", discussion_id: discussionID, message_id: "msg_question", decision_id: null, prompt: "Explain the captured retry behavior.", model: reported.model, context_id: reported.context_id, ...overrides })}\n\n`,
      );
    },
    async close() {
      await session.extensionRunner.emit({
        type: "session_shutdown",
        reason: "quit",
      });
      session.dispose();
      stream?.destroy();
      server.closeAllConnections();
      await new Promise<void>((resolve) => server.close(() => resolve()));
      if (oldServer === undefined) delete process.env.PRUNELLE_SERVER;
      else process.env.PRUNELLE_SERVER = oldServer;
      await rm(root, { recursive: true, force: true });
    },
  };
}

test("the existing Pi conversation looks up history and returns a structured correlated reply", async () => {
  const f = await fixture([
    fauxAssistantMessage("Original coding context is retained."),
    fauxAssistantMessage(
      fauxToolCall("prunelle_discussion", { discussion_id: "dis_original" }),
      { stopReason: "toolUse" },
    ),
    fauxAssistantMessage(
      fauxToolCall("prunelle_reply", {
        invocation_id: "ain_first",
        discussion_id: "dis_original",
        body: "The original snapshot retries without deduplication.",
      }),
      { stopReason: "toolUse" },
    ),
    fauxAssistantMessage(
      fauxToolCall("prunelle_reply", {
        invocation_id: "ain_second",
        discussion_id: "dis_second",
        body: "This answer belongs to the other discussion.",
      }),
      { stopReason: "toolUse" },
    ),
  ]);
  try {
    const id = f.session.sessionId;
    await f.session.prompt("Existing coding conversation");
    f.send("ain_first");
    await eventually(() =>
      f.requests.some(
        (r) => r.path === "/v1/agent-invocations/ain_first/complete",
      ),
    );
    await eventually(
      () => !f.session.isStreaming && f.requests.at(-1)?.body?.ready === true,
    );
    f.send("ain_second", "dis_second");
    await eventually(() =>
      f.requests.some(
        (r) => r.path === "/v1/agent-invocations/ain_second/complete",
      ),
    );
    await eventually(() => !f.session.isStreaming);
    assert.equal(f.session.sessionId, id);
    assert.ok(
      JSON.stringify(f.session.messages).includes(
        "Existing coding conversation",
      ),
    );
    assert.ok(
      f.requests.some((r) => r.path === "/v1/discussions/dis_original"),
    );
    const replies = f.requests.filter((r) => r.path.endsWith("/complete"));
    assert.deepEqual(
      replies.map((r) => [r.body.invocation_id, r.body.discussion_id]),
      [
        ["ain_first", "dis_original"],
        ["ain_second", "dis_second"],
      ],
    );
    assert.ok(replies.every((r) => r.author === `pi:${id}`));
    assert.deepEqual(f.errors, []);
  } finally {
    await f.close();
  }
});

test("a busy Pi rejects an incoming request instead of steering or queueing it", async () => {
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = await fixture([
    async () => {
      await waiting;
      return fauxAssistantMessage("The local task finished.");
    },
  ]);
  try {
    const local = f.session.prompt("An unrelated local coding task");
    await eventually(() => f.session.isStreaming);
    f.send("ain_busy");
    await eventually(() =>
      f.requests.some(
        (r) =>
          r.path === "/v1/agent-invocations/ain_busy/state" &&
          r.body.status === "FAILED",
      ),
    );
    release();
    await local;
    assert.equal(f.model.state.callCount, 1);
    assert.ok(!JSON.stringify(f.session.messages).includes("ain_busy"));
    assert.deepEqual(f.errors, []);
  } finally {
    release();
    await f.close();
  }
});

test("settling after review deletion releases the conversation for a later request", async () => {
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = await fixture([
    async () => {
      await waiting;
      return fauxAssistantMessage(
        "The removed review cannot receive a result.",
      );
    },
    fauxAssistantMessage(
      fauxToolCall("prunelle_reply", {
        invocation_id: "ain_after_deletion",
        discussion_id: "dis_original",
        body: "This belongs to the surviving request.",
      }),
      { stopReason: "toolUse" },
    ),
  ]);
  try {
    f.send("ain_deleted");
    await eventually(() => f.session.isStreaming);
    f.missingInvocations.add("ain_deleted");
    release();
    await eventually(
      () => !f.session.isStreaming && f.requests.at(-1)?.body?.ready === true,
    );
    f.send("ain_after_deletion");
    await eventually(() =>
      f.requests.some(
        (request) =>
          request.path === "/v1/agent-invocations/ain_after_deletion/complete",
      ),
    );
    await eventually(() => !f.session.isStreaming);
    assert.deepEqual(f.errors, []);
  } finally {
    release();
    await f.close();
  }
});

test("stale producer metadata rejects dispatch without a model call", async () => {
  const f = await fixture([]);
  try {
    for (const [id, overrides] of [
      ["ain_stale_model", { model: "different/provider" }],
      ["ain_stale_context", { context_id: "different-conversation-leaf" }],
    ] as const) {
      f.send(id, "dis_original", overrides);
      await eventually(() =>
        f.requests.some(
          (request) =>
            request.path === `/v1/agent-invocations/${id}/state` &&
            request.body.status === "FAILED",
        ),
      );
      assert.equal(
        f.model.state.callCount,
        0,
        "stale context must not start a model run",
      );
    }
    assert.deepEqual(f.errors, []);
  } finally {
    await f.close();
  }
});

test("captured source character paging reaches beyond a long line and publishes explanations", async () => {
  const f = await fixture([
    fauxAssistantMessage(
      fauxToolCall("prunelle_context", {
        invocation_id: "ain_generation",
        repository_id: "repo_fixture",
        path: "long.ts",
        offset: 60_000,
        limit: 12_000,
      }),
      { stopReason: "toolUse" },
    ),
    (context) => {
      assert.ok(
        JSON.stringify(context.messages).includes("TAIL_CAPTURED_NOT_LIVE"),
        "the model must see immutable source beyond the first 50KB",
      );
      return fauxAssistantMessage(
        fauxToolCall("prunelle_publish_review", {
          invocation_id: "ain_generation",
          topics: [
            {
              key: "long",
              title: "Captured long source",
              summary: "A source grouping",
              explanation:
                "An inferred explanation of the captured change, not a test result.",
            },
          ],
          files: [
            {
              repository_id: "repo_fixture",
              path: "long.ts",
              topic_keys: ["long"],
              topic_ids: [],
            },
          ],
        }),
        { stopReason: "toolUse" },
      );
    },
  ]);
  try {
    f.send("ain_generation", "unused", {
      intent: "PREPARE_REVIEW",
      discussion_id: null,
      message_id: null,
    });
    await eventually(() =>
      f.requests.some(
        (request) =>
          request.path === "/v1/agent-invocations/ain_generation/complete",
      ),
    );
    const publication = f.requests.find((request) =>
      request.path.endsWith("/complete"),
    )!.body;
    assert.equal(
      publication.generation.topics[0].explanation,
      "An inferred explanation of the captured change, not a test result.",
    );
    await eventually(() => !f.session.isStreaming);
    assert.deepEqual(f.errors, []);
  } finally {
    await f.close();
  }
});

test("unrelated local input invalidates correlation rather than attributing its answer to the thread", async () => {
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = await fixture([
    async () => {
      await waiting;
      return fauxAssistantMessage(
        fauxToolCall("prunelle_reply", {
          invocation_id: "ain_interrupted",
          discussion_id: "dis_original",
          body: "Must not be attributed.",
        }),
        { stopReason: "toolUse" },
      );
    },
    fauxAssistantMessage("Answering the local question instead."),
  ]);
  try {
    f.send("ain_interrupted");
    await eventually(() => f.session.isStreaming);
    const local = f.session.prompt("A new local question", {
      streamingBehavior: "steer",
    });
    await eventually(() =>
      f.requests.some(
        (r) =>
          r.path === "/v1/agent-invocations/ain_interrupted/state" &&
          r.body.status === "UNKNOWN",
      ),
    );
    release();
    await local;
    await eventually(() => !f.session.isStreaming);
    assert.equal(
      f.requests.filter((r) => r.path.endsWith("/complete")).length,
      0,
    );
    assert.ok(
      JSON.stringify(f.session.messages).includes("A new local question"),
    );
  } finally {
    release();
    await f.close();
  }
});
