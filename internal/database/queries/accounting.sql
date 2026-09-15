-- name: AuthorizeAccounting :one
SELECT u.id FROM users u JOIN tenant_members m ON m.user_id=u.id JOIN tenants t ON t.id=m.tenant_id
WHERE u.id=$1 AND m.tenant_id=$2 AND u.status='active' AND m.status='active' AND t.status='active'
FOR SHARE OF u,m,t;
-- name: LockAccountingTenant :one
SELECT id FROM tenants WHERE id=$1 FOR UPDATE;
-- name: LockAccountingBook :one
SELECT id FROM books WHERE tenant_id=$1 AND id=$2 FOR UPDATE;
-- name: ReadAccountingOperation :one
SELECT request_hash,response FROM accounting_operations WHERE tenant_id=$1 AND key=$2;
-- name: SaveAccountingOperation :exec
INSERT INTO accounting_operations(tenant_id,key,request_hash,response) VALUES ($1,$2,$3,$4);
-- name: AccountingAudit :exec
INSERT INTO accounting_audit(id,tenant_id,book_id,actor_id,action,resource_id,before_data,after_data)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: AccountingAccounts :many
SELECT a.*,COALESCE((SELECT sum(p.amount) FROM postings p WHERE p.account_id=a.id),0)::text AS balance
FROM accounts a WHERE a.tenant_id=$1 AND a.book_id=$2 AND a.role='asset' ORDER BY a.archived,a.name,a.id;
-- name: AccountingAccount :one
SELECT * FROM accounts WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: InsertAccountingAccount :one
INSERT INTO accounts(id,tenant_id,book_id,name,role,kind,currency,category_id,system_code)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;
-- name: ChangeAccountingAccount :exec
UPDATE accounts SET name=$4,archived=$5,revision=revision+1 WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: SystemAccountingAccount :one
SELECT * FROM accounts WHERE tenant_id=$1 AND book_id=$2 AND currency=$3
AND ((sqlc.narg(category_id)::uuid IS NOT NULL AND category_id=sqlc.narg(category_id)::uuid)
OR (sqlc.narg(system_code)::text IS NOT NULL AND system_code=sqlc.narg(system_code)::text));
-- name: AccountingCategories :many
SELECT * FROM categories WHERE tenant_id=$1 AND book_id=$2 ORDER BY archived,name,id;
-- name: AccountingCategory :one
SELECT * FROM categories WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: InsertAccountingCategory :one
INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (tenant_id,book_id,system_code) DO UPDATE SET system_code=EXCLUDED.system_code RETURNING *;
-- name: ChangeAccountingCategory :exec
UPDATE categories SET name=$4,name_zh=CASE WHEN name=$4 THEN name_zh ELSE '' END,archived=$5,revision=revision+1 WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: ArchiveAccountingChildren :exec
UPDATE categories SET archived=true,revision=revision+1 WHERE tenant_id=$1 AND book_id=$2 AND parent_id=$3 AND NOT archived;
-- name: AccountingCounterparties :many
SELECT * FROM counterparties WHERE tenant_id=$1 ORDER BY archived,name,id;
-- name: AccountingCounterparty :one
SELECT * FROM counterparties WHERE tenant_id=$1 AND id=$2;
-- name: InsertAccountingCounterparty :one
INSERT INTO counterparties(id,tenant_id,name) VALUES ($1,$2,$3) RETURNING *;
-- name: ChangeAccountingCounterparty :exec
UPDATE counterparties SET name=$3,archived=$4,revision=revision+1 WHERE tenant_id=$1 AND id=$2;

-- name: InsertAccountingTransaction :exec
INSERT INTO transactions(id,tenant_id,book_id,revision,status,created_by) VALUES ($1,$2,$3,1,'posted',$4);
-- name: ChangeAccountingTransaction :exec
UPDATE transactions SET revision=$4,status=$5 WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: InsertAccountingRevision :exec
INSERT INTO transaction_revisions(tenant_id,book_id,transaction_id,revision,kind,occurred_on,account_id,to_account_id,category_id,counterparty_id,amount,to_amount,original_id,data,voided,actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16);
-- name: InsertAccountingJournal :exec
INSERT INTO journal_entries(id,tenant_id,book_id,transaction_id,revision,occurred_on,valuation_currency,reversal_of)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);
-- name: InsertAccountingPosting :exec
INSERT INTO postings(id,tenant_id,book_id,journal_id,account_id,amount,valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);
-- name: SealAccountingJournal :exec
UPDATE journal_entries SET sealed=true WHERE id=$1;
-- name: CurrentAccountingJournal :one
SELECT j.* FROM journal_entries j WHERE j.transaction_id=$1 AND j.revision=$2 AND j.reversal_of IS NULL;
-- name: AccountingPostings :many
SELECT * FROM postings WHERE journal_id=$1 ORDER BY id;
-- name: AccountingTransaction :one
SELECT accounting_transaction_json(t)::jsonb AS document FROM transactions t WHERE t.tenant_id=$1 AND t.book_id=$2 AND t.id=$3;
-- name: AccountingHistory :many
SELECT revision,voided,data,actor_id,created_at FROM transaction_revisions
WHERE tenant_id=$1 AND book_id=$2 AND transaction_id=$3 ORDER BY revision DESC;
-- name: AccountingJournals :many
SELECT jsonb_build_object('id',j.id,'revision',j.revision,'occurred_on',j.occurred_on,
'valuation_currency',j.valuation_currency,'reversal_of',j.reversal_of,
'postings',COALESCE((SELECT jsonb_agg(jsonb_build_object('account_id',p.account_id,'currency',a.currency,
'amount',p.amount::text,'valuation_amount',p.valuation_amount::text,'rate_numerator',p.rate_numerator::text,
'rate_denominator',p.rate_denominator::text,'rate_source',p.rate_source,'rate_date',p.rate_date) ORDER BY p.id)
FROM postings p JOIN accounts a ON a.id=p.account_id WHERE p.journal_id=j.id),'[]'::jsonb))::jsonb AS document
FROM journal_entries j WHERE j.tenant_id=$1 AND j.book_id=$2 AND j.transaction_id=$3 ORDER BY j.revision DESC,j.reversal_of NULLS LAST,j.id;
-- name: AccountingTransactions :many
SELECT accounting_transaction_json(t)::jsonb AS document FROM transactions t
JOIN transaction_revisions r ON r.transaction_id=t.id AND r.revision=t.revision
WHERE t.tenant_id=sqlc.arg(tenant_id) AND t.book_id=sqlc.arg(book_id)
AND (sqlc.arg(include_voided)::boolean OR t.status='posted')
AND (sqlc.narg(from_date)::date IS NULL OR r.occurred_on>=sqlc.narg(from_date)::date)
AND (sqlc.narg(to_date)::date IS NULL OR r.occurred_on<=sqlc.narg(to_date)::date)
AND (sqlc.narg(kind)::text IS NULL OR r.kind=sqlc.narg(kind)::text)
AND (sqlc.narg(account_id)::uuid IS NULL OR r.account_id=sqlc.narg(account_id)::uuid OR r.to_account_id=sqlc.narg(account_id)::uuid)
AND (sqlc.narg(currency)::text IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id IN (r.account_id,r.to_account_id) AND a.currency=sqlc.narg(currency)::text))
AND (sqlc.narg(category_id)::uuid IS NULL OR r.category_id=sqlc.narg(category_id)::uuid OR EXISTS (SELECT 1 FROM categories c WHERE c.id=r.category_id AND c.parent_id=sqlc.narg(category_id)::uuid))
AND (sqlc.narg(counterparty_id)::uuid IS NULL OR r.counterparty_id=sqlc.narg(counterparty_id)::uuid)
AND (sqlc.narg(cursor_date)::date IS NULL OR (r.occurred_on,t.created_at,t.id)<(sqlc.narg(cursor_date)::date,sqlc.narg(cursor_time)::timestamptz,sqlc.narg(cursor_id)::uuid))
ORDER BY r.occurred_on DESC,t.created_at DESC,t.id DESC LIMIT sqlc.arg(page_size);
-- name: AccountingRefunded :one
SELECT COALESCE(sum(r.amount),0)::text AS amount FROM transaction_links l
JOIN transactions t ON t.id=l.target_id JOIN transaction_revisions r ON r.transaction_id=t.id AND r.revision=t.revision
WHERE l.source_id=$1 AND l.kind='refund' AND t.status='posted' AND t.id<>$2;
-- name: AccountingOpeningExists :one
SELECT EXISTS(SELECT 1 FROM transactions t JOIN transaction_revisions r ON r.transaction_id=t.id AND r.revision=t.revision
WHERE r.account_id=$1 AND r.kind='opening' AND t.status='posted');
-- name: InsertAccountingLink :one
INSERT INTO transaction_links(id,tenant_id,book_id,source_id,target_id,kind,created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *;
-- name: AccountingLinks :many
SELECT * FROM transaction_links WHERE tenant_id=$1 AND book_id=$2 AND ($3::uuid IS NULL OR source_id=$3 OR target_id=$3) ORDER BY created_at,id;
-- name: AccountingPageLinks :many
SELECT * FROM transaction_links WHERE tenant_id=$1 AND book_id=$2 AND (source_id=ANY(sqlc.arg(transaction_ids)::uuid[]) OR target_id=ANY(sqlc.arg(transaction_ids)::uuid[])) ORDER BY created_at,id;
-- name: AccountingLink :one
SELECT * FROM transaction_links WHERE tenant_id=$1 AND book_id=$2 AND id=$3;
-- name: RemoveAccountingLink :exec
DELETE FROM transaction_links WHERE tenant_id=$1 AND book_id=$2 AND id=$3 AND kind='related';
-- name: AccountingSummary :many
SELECT a.currency,a.role,COALESCE(a.category_id,'00000000-0000-0000-0000-000000000000'::uuid)::uuid AS category_id,
sum(CASE WHEN a.role='income' THEN -p.amount ELSE p.amount END)::text AS amount
FROM postings p JOIN journal_entries j ON j.id=p.journal_id JOIN accounts a ON a.id=p.account_id
WHERE p.tenant_id=$1 AND p.book_id=$2 AND a.role IN ('income','expense')
AND (sqlc.narg(from_date)::date IS NULL OR j.occurred_on>=sqlc.narg(from_date)::date)
AND (sqlc.narg(to_date)::date IS NULL OR j.occurred_on<=sqlc.narg(to_date)::date)
GROUP BY a.currency,a.role,a.category_id ORDER BY a.currency,a.role,a.category_id;
