# kvnode

Very simple key value store.

- Redis API
- LevelDB storage
- Raft support

Commands:

```
SET key value
GET key
DEL key [key ...]
KEYS pattern [PIVOT prefix] [LIMIT count] [DESC]
FLUSHDB
SHUTDOWN
```

## Contact
Josh Baker [@tidwall](http://twitter.com/tidwall)

## License
kvnode source code is available under the MIT [License](/LICENSE).
