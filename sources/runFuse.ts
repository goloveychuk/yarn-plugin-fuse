//@ts-expect-error
import checksums from '../fuse/output/checksums.json';
import * as os from 'os';
import * as path from 'path';
import * as fs from 'fs';
import * as crypto from 'crypto';
import { spawn } from 'child_process';
import { pipeline } from 'stream/promises';
import * as http from 'http';
import {setInterval, setTimeout} from 'timers/promises'

http.request;

const archMap = {
  x64: 'amd64',
};

async function checkChecksum(p: string, checksum: string) {
  const hash = crypto.createHash('sha512');
  const stream = fs.createReadStream(p);
  await pipeline(stream, hash);
  const hashRes = hash.digest('hex');
  if (hashRes !== checksum) {
    throw new Error(`Checksum mismatch for ${p}`);
  }
}

class Api {
  constructor(private socketPath: string) {}
  async waitToInit() {
    const deadline = Date.now() + 10000;
    for await (const _ of setInterval(100)) {
        if (Date.now() > deadline) {
            throw new Error('Timeout waiting for fuse to start');
        }
        try {
            await this.request();
            return;
        } catch (e) {
            // ignore
        }
    }
  }
  async request<T>(path: string = '/'): Promise<T> {
    return new Promise((resolve, reject) => {
      const req = http.request({
        socketPath: this.socketPath,
        path,
        timeout: 1000,
      });
      req.on('response', (res) => {
        let data = '';
        res.on('data', (chunk) => {
          data += chunk;
        });
        res.on('end', () => {
          resolve(JSON.parse(data));
        });
      });
      req.on('error', (err) => {
        reject(err);
      });
      req.end();
    });
  }
}

export async function runFuse(confPath: string) {
  const controlPath = confPath + '.control';
//   if (fs.existsSync(controlPath)) {
    const api = new Api(controlPath);
    
  
  let arch = os.arch();
  const platform = os.platform();
  if (archMap[arch]) {
    arch = archMap[arch];
  }
  const name = `fuse-${platform}-${arch}`;
  const checksum = checksums[name];
  if (!checksum) {
    throw new Error(`No checksum found for ${name}`);
  }

  const file = path.join(
    '/Users/vadymh/github/yarn-plugin-fuse/fuse/output',
    name,
  );
  await checkChecksum(file, checksum);
  const child = spawn(file, [confPath], {
    detached: true,
    stdio: 'inherit',
  });
  child.unref();

  await setTimeout(5000)

    // await api.waitToInit();

}
