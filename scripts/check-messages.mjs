// Validate Vue message syntax and structural controls with its actual compiler.
import { createRequire } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const [source, translated, upstream, output] = process.argv.slice(2);
const require = createRequire(resolve(upstream, 'package.json'));
const { baseCompile } = require('@intlify/message-compiler');
function flatten(value, prefix = '', result = {}) {
    for (const [key, item] of Object.entries(value)) {
        const name = prefix ? `${prefix}.${key}` : key;
        if (typeof item === 'string') result[name] = item;
        else if (item && typeof item === 'object' && !Array.isArray(item)) flatten(item, name, result);
        else throw new Error(`Invalid message: ${name}`);
    }
    return result;
}
const english = flatten(JSON.parse(readFileSync(source, 'utf8')));
const target = flatten(JSON.parse(readFileSync(translated, 'utf8')));

function signature(message, key) {
    const { ast } = baseCompile(message, {
        onError(error) { throw new Error(`${key}: ${error.message}`); },
    });
    const links = [];
    const parameters = [];
    let plurals = 1;
    function visit(node) {
        if (!node || typeof node !== 'object') return;
        if (node.type === 1) plurals = node.cases.length;
        if (node.type === 4) parameters.push(`named:${node.key}`);
        if (node.type === 5) parameters.push(`list:${node.index}`);
        if (node.type === 6) links.push(`${node.modifier?.value ?? ''}:${node.key?.value ?? ''}`);
        for (const value of Object.values(node)) {
            if (Array.isArray(value)) value.forEach(visit);
            else if (value && typeof value === 'object') visit(value);
        }
    }
    visit(ast);
    // HTML control tags retain their names and attributes, not translated text.
    const tags = [...message.matchAll(/<\/?[A-Za-z][^>]*>/g)].map(match => match[0]).sort();
    return { plurals, links: links.sort(), parameters: parameters.sort(), tags };
}

const errors = [];
for (const [key, message] of Object.entries(english)) {
    try {
        if (typeof message !== 'string' || typeof target[key] !== 'string') throw new Error('message must be text');
        if (JSON.stringify(signature(message, key)) !== JSON.stringify(signature(target[key], key))) {
            errors.push({ key, error: 'plural, linked message, interpolation or HTML controls changed' });
        }
    } catch (error) { errors.push({ key, error: error.message }); }
}
writeFileSync(output, JSON.stringify({ schema: 1, keys: Object.keys(english).length, errors }, null, 2) + '\n');
if (errors.length) throw new Error(`${errors.length} locale syntax/structure errors; see ${output}`);
