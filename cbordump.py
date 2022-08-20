#!/usr/bin/env python3
#
# pip install cbor

import base64
import json
import sys

import cbor

bstdin = open(sys.stdin.fileno(), 'rb')

_sp = ord(' ')

def maybestr(x):
        try:
                y = x.decode()
                for cs in y:
                        c = ord(cs)
                        if 0x20 <= c <= 0x7f:
                                pass
                        else:
                                return None
                return y
        except:
                return None

def jsonable(x):
        if isinstance(x, dict):
                out = {}
                for k, v in x.items():
                        if isinstance(v, bytes):
                                xs = maybestr(v)
                                if xs:
                                        out[k] = xs
                                else:
                                        out[k + '_b64'] = base64.b64encode(v).decode()
                        else:
                                out[k] = jsonable(v)
                return out
        return x

while True:
	ob = cbor.cbor.load(bstdin)
	json.dump(jsonable(ob), sys.stdout)
	sys.stdout.write('\n')
