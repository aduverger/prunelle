import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import type { Model } from '../src/api/client';

export const server = 'http://127.0.0.1:17391';
export class Fixture {
  dir = mkdtempSync(join(tmpdir(), 'prunelle-ui-'));
  config = join(this.dir, 'config.toml');
  process?: ChildProcess;
  review!: Model['Review'];
  revision!: Model['Revision'];
  ids!: Record<string, Record<string, string>>;
  log = '';
  binary: string;
  constructor(binary: string) {
    this.binary = binary;
    writeFileSync(
      this.config,
      `listen = "127.0.0.1:17391"
data_dir = "${this.dir}/data"
`,
    );
  }
  async start() {
    this.process = spawn(this.binary, ['--config', this.config, 'serve'], {
      env: { ...process.env, PRUNELLE_AGENT: '' },
    });
    this.process.stderr!.on('data', (chunk) => {
      this.log += chunk;
    });
    for (let i = 0; i < 150; i++) {
      if (this.process.exitCode !== null) throw new Error(this.log);
      try {
        if ((await fetch(server + '/v1/health')).ok) return;
      } catch {
        /* Starting. */
      }
      await new Promise((r) => setTimeout(r, 20));
    }
    throw new Error('Server did not start: ' + this.log);
  }
  async seed() {
    const output = execFileSync(
      this.binary,
      ['--config', this.config, 'fixture', join(this.dir, 'repos')],
      { encoding: 'utf8', env: { ...process.env, PRUNELLE_AGENT: '' } },
    );
    this.ids = JSON.parse(output).ids;
    this.review = (await this.get<Model['Review'][]>('/v1/reviews'))[0];
    this.revision = (
      await this.get<Model['Revision'][]>('/v1/reviews/' + this.review.id + '/revisions')
    )[0];
  }
  async get<T>(path: string): Promise<T> {
    const res = await fetch(server + path);
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  }
  async stop() {
    if (!this.process || this.process.exitCode !== null) return;
    const exited = new Promise<void>((resolve) => this.process!.once('exit', () => resolve()));
    this.process.kill('SIGTERM');
    await exited;
  }
  async close() {
    await this.stop();
    rmSync(this.dir, { recursive: true, force: true });
  }
}
export function buildServer() {
  const dir = mkdtempSync(join(tmpdir(), 'prunelle-ui-bin-'));
  const binary = join(dir, 'prunelle');
  execFileSync('go', ['build', '-o', binary, './cmd/prunelle'], { cwd: resolve('../server') });
  return { binary, close: () => rmSync(dir, { recursive: true, force: true }) };
}
