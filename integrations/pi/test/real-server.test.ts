import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { test } from "node:test";
import {
  fauxAssistantMessage,
  fauxProvider,
  fauxToolCall,
} from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";
import bridge from "../src/index.ts";

test(
  "the actual Go server and Pi extension preserve conversation, attachment, thread and committed replay",
  { timeout: 60000 },
  async () => {
    const root = await mkdtemp(join(tmpdir(), "prunelle-pi-server-"));
    const moduleRoot = fileURLToPath(
      new URL("../../../server/", import.meta.url),
    );
    const binary = join(root, "prunelle");
    const built = spawnSync("go", ["build", "-o", binary, "./cmd/prunelle"], {
      cwd: moduleRoot,
      encoding: "utf8",
    });
    assert.equal(built.status, 0, built.stderr);
    const config = join(root, "config.toml");
    await writeFile(
      config,
      `listen = "127.0.0.1:0"\ndata_dir = ${JSON.stringify(join(root, "data"))}\n`,
    );
    const server = spawn(binary, ["--config", config, "serve"], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    let logs = "";
    const url = await new Promise<string>((resolve, reject) => {
      server.stderr.on("data", (data) => {
        logs += String(data);
        const address = logs.match(/listen=(127\.0\.0\.1:\d+)/);
        if (address) resolve(`http://${address[1]}`);
      });
      server.once("exit", (code) =>
        reject(new Error(`Go server exited ${code}: ${logs}`)),
      );
    });
    const oldServer = process.env.PRUNELLE_SERVER;
    process.env.PRUNELLE_SERVER = url;
    const json = async (
      path: string,
      method = "GET",
      body?: unknown,
      headers: Record<string, string> = {},
    ) => {
      const response = await fetch(`${url}${path}`, {
        method,
        headers: { "Content-Type": "application/json", ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const result = await response.json();
      assert.ok(response.ok, `${response.status}: ${JSON.stringify(result)}`);
      return { result, response };
    };
    let session:
      Awaited<ReturnType<typeof createAgentSession>>["session"] | undefined;
    try {
      const repos = join(root, "repos");
      const env: NodeJS.ProcessEnv = {
        ...process.env,
        PRUNELLE_AGENT: "",
        GIT_CONFIG_GLOBAL: "/dev/null",
        GIT_CONFIG_NOSYSTEM: "1",
        GIT_CONFIG_COUNT: "1",
        GIT_CONFIG_KEY_0: "user.useConfigOnly",
        GIT_CONFIG_VALUE_0: "true",
      };
      for (const key of [
        "GIT_AUTHOR_NAME",
        "GIT_AUTHOR_EMAIL",
        "GIT_COMMITTER_NAME",
        "GIT_COMMITTER_EMAIL",
        "EMAIL",
      ])
        delete env[key];
      const seeded = spawnSync(
        binary,
        ["--config", config, "fixture", "--server", url, repos],
        { env, encoding: "utf8" },
      );
      assert.equal(seeded.status, 0, seeded.stderr);
      const cli = (args: string[], invocationCwd = repos) => {
        const output = spawnSync(
          binary,
          [
            "--config",
            config,
            "agent",
            args[0],
            "--server",
            url,
            ...args.slice(1),
          ],
          { cwd: invocationCwd, env, encoding: "utf8" },
        );
        assert.equal(output.status, 0, output.stderr);
        return JSON.parse(output.stdout);
      };
      const review = (await json("/v1/reviews")).result[0];
      const discussion = (
        await json(`/v1/reviews/${review.id}/discussions`, "POST", {
          anchor: { type: "REVIEW" },
          body: "Explain retry behavior from the captured revision.",
        })
      ).result;
      const cwd = join(repos, "api");
      const agentDir = join(root, "pi");
      await mkdir(agentDir);
      const model = fauxProvider({ provider: "prunelle-real-server-fixture" });
      model.setResponses([
        (context) => {
          const request = JSON.stringify(context.messages).match(
            /Prunelle DISCUSS request ([^\s"\\]+)/,
          );
          assert.ok(
            request,
            "the existing Pi context must contain the explicit invocation",
          );
          return fauxAssistantMessage(
            fauxToolCall("prunelle_reply", {
              invocation_id: request[1],
              discussion_id: discussion.id,
              body: "The captured worker can retry; this is an explanation, not verification or a code change.",
            }),
            { stopReason: "toolUse" },
          );
        },
        (context) => {
          const request = JSON.stringify(context.messages).match(
            /Prunelle PREPARE_REVIEW request ([^\s"\\]+)/,
          );
          assert.ok(request);
          return fauxAssistantMessage(
            fauxToolCall("prunelle_context", {
              invocation_id: request[1],
              repository_id: review.repositories.find(
                (repository: any) => repository.label === "api",
              ).id,
              path: "package.json",
            }),
            { stopReason: "toolUse" },
          );
        },
        (context) => {
          const text = JSON.stringify(context.messages);
          assert.ok(
            text.includes("CAPTURED HEAD"),
            "the model must consume the server-captured file through the extension",
          );
          const request = text.match(
            /Prunelle PREPARE_REVIEW request ([^\s"\\]+)/,
          );
          assert.ok(request);
          return fauxAssistantMessage(
            fauxToolCall("prunelle_publish_review", {
              invocation_id: request[1],
              topics: [
                {
                  key: "dependencies",
                  title: "Generated dependency review",
                  summary: "Dependency changes in the captured feature",
                  explanation:
                    "The captured package changes are grouped here. This is an inferred explanation, not measured test evidence.",
                },
              ],
              files: [
                {
                  repository_id: review.repositories.find(
                    (repository: any) => repository.label === "api",
                  ).id,
                  path: "package.json",
                  topic_keys: ["dependencies"],
                  topic_ids: [],
                },
              ],
            }),
            { stopReason: "toolUse" },
          );
        },
        (context) => {
          const request = JSON.stringify(context.messages).match(
            /Prunelle IMPLEMENT request ([^\s"\\]+)/,
          );
          assert.ok(request);
          return fauxAssistantMessage(
            fauxToolCall("prunelle_reply", {
              invocation_id: request[1],
              body: "Controlled fixture handoff received. No project changes, verdict, capture, commit or publication were performed.",
            }),
            { stopReason: "toolUse" },
          );
        },
      ]);
      const runtime = await ModelRuntime.create({
        authPath: join(agentDir, "auth.json"),
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
        cwd,
        agentDir,
        settingsManager: settings,
        noExtensions: true,
        noSkills: true,
        noThemes: true,
        noPromptTemplates: true,
        noContextFiles: true,
        extensionFactories: [bridge],
      });
      await loader.reload();
      ({ session } = await createAgentSession({
        cwd,
        agentDir,
        model: model.getModel(),
        modelRuntime: runtime,
        settingsManager: settings,
        resourceLoader: loader,
        tools: ["prunelle_*"],
        sessionManager: SessionManager.inMemory(cwd),
      }));
      const sessionID = session.sessionId;
      const errors: string[] = [];
      await session.bindExtensions({
        onError: (error) => errors.push(error.error),
      });
      const waitFor = async (predicate: () => Promise<boolean>) => {
        const deadline = Date.now() + 5000;
        while (!(await predicate())) {
          assert.ok(
            Date.now() < deadline,
            `real bridge did not reach expected state: ${logs}; ${errors.join("; ")}`,
          );
          await delay(15);
        }
      };
      await waitFor(async () =>
        (await json("/v1/agent-sessions")).result.some(
          (s: any) => s.external_id === sessionID && s.state === "READY",
        ),
      );
      const binding = cli(["binding"], join(repos, "worker"));
      assert.ok(
        cli(["ls"]).some((source: any) => source.external_id === sessionID),
      );
      assert.ok(
        binding.agent_session_id,
        "the unique local cwd match should be attached",
      );
      const ask = [
        "ask",
        "--review",
        review.id,
        "--revision",
        review.current_revision_id,
        "--key",
        "real-pi-question",
        discussion.id,
        discussion.messages[0].id,
      ];
      const first = { result: cli(ask) };
      await waitFor(
        async () =>
          (await json(`/v1/agent-invocations/${first.result.id}`)).result
            .status === "SUCCEEDED",
      );
      const replied = (await json(`/v1/discussions/${discussion.id}`)).result;
      assert.equal(replied.messages.length, 2);
      assert.equal(replied.messages[1].author.kind, "AGENT");
      assert.equal(replied.messages[1].author.id, `pi:${sessionID}`);
      assert.ok(replied.messages[1].body.includes("captured worker"));
      assert.deepEqual(cli(ask), first.result);
      assert.equal(cli(["get", first.result.id]).status, "SUCCEEDED");
      assert.equal(model.state.callCount, 1);
      await waitFor(async () =>
        (await json("/v1/agent-sessions")).result.some(
          (source: any) =>
            source.id === binding.agent_session_id && source.state === "READY",
        ),
      );
      const prepared = cli([
        "prepare",
        "--review",
        review.id,
        "--key",
        "real-pi-generation",
      ]);
      await waitFor(
        async () =>
          (await json(`/v1/agent-invocations/${prepared.id}`)).result.status ===
          "SUCCEEDED",
      );
      const generated = (await json(`/v1/agent-invocations/${prepared.id}`))
        .result;
      const topic = (
        await json(`/v1/topics/${generated.result.topic_ids.dependencies}`)
      ).result;
      assert.equal(topic.title, "Generated dependency review");
      assert.equal(topic.origin, "GENERATED");
      assert.equal(topic.author.id, `pi:${sessionID}`);
      const representations = (
        await json(`/v1/topics/${topic.id}/representations`)
      ).result;
      assert.ok(
        representations.some(
          (representation: any) =>
            representation.type === "TEXT" &&
            representation.text.sections[0].body.includes(
              "not measured test evidence",
            ),
        ),
      );
      assert.ok(
        representations.some(
          (representation: any) =>
            representation.type === "CODE_DIFF" &&
            representation.source_files.some(
              (file: any) => file.path === "package.json",
            ),
        ),
      );
      await waitFor(async () =>
        (await json("/v1/agent-sessions")).result.some(
          (source: any) =>
            source.id === binding.agent_session_id && source.state === "READY",
        ),
      );
      const saved = (
        await json(`/v1/reviews/${review.id}/decisions`, "POST", {
          revision_id: review.current_revision_id,
          decision: "REQUEST_CHANGES",
          body: "Keep the original actionable thread when implementing.",
          finding_ids: [],
          discussion_ids: [discussion.id],
        })
      ).result;
      assert.equal(
        model.state.callCount,
        3,
        "saving a verdict must not invoke Pi",
      );
      const preview = cli(["implement", "--review", review.id, saved.id]);
      assert.equal(preview.decision.id, saved.id);
      assert.equal(
        preview.original_discussion_anchors[0].discussion_id,
        discussion.id,
      );
      assert.equal(preview.request.revision_id, saved.revision_id);
      assert.equal(model.state.callCount, 3, "a CLI preview must not dispatch");
      const unauthorizedTarget = spawnSync(
        binary,
        [
          "--config",
          config,
          "agent",
          "implement",
          "--server",
          url,
          "--review",
          review.id,
          "--yes",
          saved.id,
        ],
        { env, encoding: "utf8" },
      );
      assert.notEqual(
        unauthorizedTarget.status,
        0,
        "implementation requires an explicit reviewed Pi target",
      );
      const implementation = cli([
        "implement",
        "--review",
        review.id,
        "--key",
        "real-pi-implementation",
        "--yes",
        "--agent-session",
        binding.agent_session_id,
        saved.id,
      ]);
      await waitFor(
        async () =>
          (await json(`/v1/agent-invocations/${implementation.id}`)).result
            .status === "SUCCEEDED",
      );
      assert.equal(model.state.callCount, 4);
      assert.equal(
        (await json(`/v1/reviews/${review.id}`)).result.current_revision_id,
        review.current_revision_id,
      );
      assert.equal(
        (await json(`/v1/reviews/${review.id}/decisions`)).result.length,
        1,
      );
      assert.equal(
        (await json(`/v1/discussions/${discussion.id}`)).result.status,
        "OPEN",
      );
      assert.equal(cli(["requests", "--review", review.id]).length, 3);
      assert.equal(
        cli(["disconnect", "--review", review.id]).mode,
        "DISCONNECTED",
      );
      assert.equal(
        cli(["select", "--review", review.id, binding.agent_session_id]).mode,
        "MANUAL",
      );
      assert.equal(
        cli(["auto", "--review", review.id]).agent_session_id,
        binding.agent_session_id,
      );
      assert.equal(
        model.state.callCount,
        4,
        "attachment controls must not dispatch work",
      );
      assert.equal(session.sessionId, sessionID);
      assert.deepEqual(errors, []);
    } finally {
      if (session) {
        await session.extensionRunner.emit({
          type: "session_shutdown",
          reason: "quit",
        });
        session.dispose();
      }
      if (oldServer === undefined) delete process.env.PRUNELLE_SERVER;
      else process.env.PRUNELLE_SERVER = oldServer;
      const exited = once(server, "exit");
      server.kill("SIGTERM");
      await exited;
      await rm(root, { recursive: true, force: true });
    }
  },
);
