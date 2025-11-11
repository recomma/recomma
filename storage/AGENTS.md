# package storage

If you need to query new data, do not do this directly. Add a new query to `./sqlc/queries.sql` and generate with `sqlc` the resulting typed query.
Schema changes go inside `./sqlc/schema.sql` and generate the new models with `sqlc` too.

The generation can be triggered from the current working dir with `go generate ../gen/storage`

*DO NOT EMBED SQL QUERIES INSIDE THE `storage` CLIENT*