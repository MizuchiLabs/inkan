// Zips the files of a folder, flat, the way a function upload wants them.
// Node has no zip of its own and the zip tool is not on every machine.
//
//   node scripts/zip.ts <out.zip> <folder>

import { readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { crc32, deflateRawSync } from 'node:zlib';

const [out, dir] = process.argv.slice(2);
if (!out || !dir) throw new Error('usage: node scripts/zip.ts <out.zip> <folder>');

const now = new Date();
const dosTime = (now.getHours() << 11) | (now.getMinutes() << 5) | (now.getSeconds() >> 1);
const dosDate = ((now.getFullYear() - 1980) << 9) | ((now.getMonth() + 1) << 5) | now.getDate();

const locals: Buffer[] = [];
const central: Buffer[] = [];
let offset = 0;
for (const name of readdirSync(dir).sort()) {
	const data = readFileSync(join(dir, name));
	const packed = deflateRawSync(data);
	const file = Buffer.from(name);
	const header = Buffer.alloc(30);
	header.writeUInt32LE(0x04034b50, 0);
	header.writeUInt16LE(20, 4);
	header.writeUInt16LE(0, 6);
	header.writeUInt16LE(8, 8);
	header.writeUInt16LE(dosTime, 10);
	header.writeUInt16LE(dosDate, 12);
	header.writeUInt32LE(crc32(data), 14);
	header.writeUInt32LE(packed.length, 18);
	header.writeUInt32LE(data.length, 22);
	header.writeUInt16LE(file.length, 26);
	header.writeUInt16LE(0, 28);
	locals.push(header, file, packed);

	const entry = Buffer.alloc(46);
	entry.writeUInt32LE(0x02014b50, 0);
	entry.writeUInt16LE(20, 4);
	entry.writeUInt16LE(20, 6);
	entry.writeUInt16LE(0, 8);
	entry.writeUInt16LE(8, 10);
	entry.writeUInt16LE(dosTime, 12);
	entry.writeUInt16LE(dosDate, 14);
	entry.writeUInt32LE(crc32(data), 16);
	entry.writeUInt32LE(packed.length, 20);
	entry.writeUInt32LE(data.length, 24);
	entry.writeUInt16LE(file.length, 28);
	entry.writeUInt16LE(0, 30);
	entry.writeUInt16LE(0, 32);
	entry.writeUInt16LE(0, 34);
	entry.writeUInt16LE(0, 36);
	entry.writeUInt32LE(0, 38);
	entry.writeUInt32LE(offset, 42);
	central.push(entry, file);
	offset += header.length + file.length + packed.length;
}

const directory = Buffer.concat(central);
const end = Buffer.alloc(22);
end.writeUInt32LE(0x06054b50, 0);
end.writeUInt16LE(0, 4);
end.writeUInt16LE(0, 6);
end.writeUInt16LE(locals.length / 3, 8);
end.writeUInt16LE(locals.length / 3, 10);
end.writeUInt32LE(directory.length, 12);
end.writeUInt32LE(offset, 16);
end.writeUInt16LE(0, 20);
writeFileSync(out, Buffer.concat([...locals, directory, end]));
console.log(`wrote ${out}`);
