import * as path from 'path';
import * as fs from 'fs/promises';
import * as crypto from 'crypto';
import { spawn } from 'child_process';
import { pipeline } from 'stream/promises';
import { setInterval } from 'timers/promises';
import { xfs, ppath, PortablePath } from '@yarnpkg/fslib';
import { getExecFileName } from '../utils.mjs';
import metadata from '../fuse/output/metadata.json';
import { fileURLToPath } from 'url';
import * as os from 'os';

async function checkChecksum(p: string, checksum: string) {
  const hash = crypto.createHash('sha512');
  const stream = await fs.open(p, 'r');
  await pipeline(stream.createReadStream(), hash);
  const hashRes = hash.digest('hex');
  if (hashRes !== checksum) {
    throw new Error(`Checksum mismatch for ${p}`);
  }
}
const MAGIC_PATH = '.00unmount';

export async function unmountFuse(nmPath: PortablePath) {
  const p = ppath.join(nmPath, MAGIC_PATH);
  if (await xfs.existsPromise(p)) {
    await xfs.removePromise(p, {});
  }
}

async function waitToMount(nmPath: PortablePath) {
  const p = ppath.join(nmPath, MAGIC_PATH);
  const deadLine = Date.now() + 10_000;
  for await (const _ of setInterval(300)) {
    if (Date.now() > deadLine) {
      throw new Error('Timeout waiting for fuse to mount');
    }
    if (await xfs.existsPromise(p)) {
      break;
    }
  }
}

async function downloadFile(url: string) {
  const tmpPath = path.join(os.tmpdir(), crypto.randomUUID());
  const resp = await fetch(url)
  if (!resp.ok) {
    throw new Error(`Failed to download ${url}`);
  }
  if (!resp.body) {
    throw new Error(`No body for ${url}`);
  }
  const handle = await fs.open(tmpPath, 'w')
  await pipeline(resp.body, handle.createWriteStream());
  return tmpPath;
}

async function downloadFileOrCache(url: string, key: string): Promise<string> {
  const resultPath = path.join(os.tmpdir(), key);
  if (await (fs.stat(resultPath).catch(() => false))) {
    return resultPath;
  }
  const newPath = await downloadFile(url);
  await fs.rename(newPath, resultPath);
  throw new Error('Not implemented');
}

export async function runFuse(nmPath: PortablePath, confPath: string) {
  const name = getExecFileName() as keyof typeof metadata;
  const meta = metadata[name];
  if (!meta) {
    throw new Error(`No checksum found for ${name}`);
  }
  const filePath = new URL(meta.path);
  let realFilePath: string;
  if (filePath.protocol === 'file:') {
    realFilePath = fileURLToPath(filePath);
  } else {
    realFilePath = await downloadFileOrCache(filePath.href, meta.checksum);
  }
  await checkChecksum(realFilePath, meta.checksum);
  const info = os.userInfo();
  const child = spawn('sudo', [realFilePath, '-uid', String(info.uid), '-gid', String(info.gid), confPath], {
    detached: true,
    stdio: 'inherit',
  });
  child.unref();
  console.log("PID", child.pid)
  await waitToMount(nmPath);

  // await api.waitToInit();
}
