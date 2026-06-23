#!/usr/bin/env node
// install.js — downloads the correct ok binary for the current platform
// from the latest GitHub release. Part of the @colbymchenry/ok npm package.
const https = require('https');
const fs = require('fs');
const path = require('path');

const pkg = require('./package.json');
const version = pkg.version;
const platform = process.platform;
const arch = process.arch;

function mapArch(a) {
	if (a === 'x64') return 'amd64';
	if (a === 'arm64') return 'arm64';
	throw new Error('unsupported arch: ' + a);
}

function mapPlatform(p) {
	if (p === 'win32') return 'windows';
	if (p === 'darwin') return 'darwin';
	if (p === 'linux') return 'linux';
	throw new Error('unsupported platform: ' + p);
}

const os = mapPlatform(platform);
const a = mapArch(arch);
const ext = os === 'windows' ? '.zip' : '.tar.gz';
const binExt = os === 'windows' ? '.exe' : '';
const binName = 'ok' + binExt;

const url = `https://github.com/colbymchenry/ok/releases/download/v${version}/ok-${os}-${a}${ext}`;
const binDir = path.join(__dirname, 'bin');
const binPath = path.join(binDir, binName);

if (!fs.existsSync(binDir)) {
	fs.mkdirSync(binDir, { recursive: true });
}

console.log('Downloading ok ' + version + ' for ' + os + '/' + a + '...');

const file = fs.createWriteStream(binPath + '.tmp');
https.get(url, function (response) {
	if (response.statusCode !== 200) {
		fs.unlinkSync(binPath + '.tmp');
		console.error('Download failed: HTTP ' + response.statusCode);
		process.exit(1);
	}
	response.pipe(file);
	file.on('finish', function () {
		file.close();
		fs.renameSync(binPath + '.tmp', binPath);
		fs.chmodSync(binPath, 0o755);
		console.log('Installed ' + binPath);
	});
}).on('error', function (err) {
	fs.unlinkSync(binPath + '.tmp');
	console.error('Download failed:', err.message);
	process.exit(1);
});
