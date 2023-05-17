import * as path from 'path';
import * as fs from 'fs';
import * as crypto from 'crypto';
import { spawn } from 'child_process';
import { pipeline } from 'stream/promises';
import { setInterval } from 'timers/promises';
import { xfs, ppath, PortablePath } from '@yarnpkg/fslib';
import { getExecFileName } from '../utils.mjs';
import metadata from '../fuse/output/metadata.json';
import { fileURLToPath } from 'url';
import * as os from 'os';
import * as https from 'https';

async function checkChecksum(p: string, checksum: string) {
  const hash = crypto.createHash('sha512');
  const stream = fs.createReadStream(p);
  await pipeline(stream, hash);
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

function downloadFile(url: string, dest: string) {
  const tmpPath = path.join(os.tmpdir(), crypto.randomUUID());
  return new Promise<void>((resolve, reject) => {
    const file = fs.createWriteStream(tmpPath);
    const req = https.get(url, (res) => {
      res.pipe(file);
      file.on('finish', () => {
        file.close();
        fs.rename(tmpPath, dest, (err) => {
          if (err) {
            reject(err);
          } else {
            resolve();
          }
        });
      });
    });
    req.on('error', (err) => {
      reject(err);
    });
    req.end();
  });
}

async function downloadFileOrCache(url: string): Promise<string> {
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
    realFilePath = await downloadFileOrCache(filePath.href);
  }
  await checkChecksum(realFilePath, meta.checksum);
  const child = spawn(realFilePath, [confPath], {
    detached: true,
    stdio: 'inherit',
  });
  child.unref();

  await waitToMount(nmPath);

  // await api.waitToInit();
}
