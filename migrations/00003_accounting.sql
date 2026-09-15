-- +goose Up
CREATE TABLE currencies (
 code text PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
 minor_units integer NOT NULL CHECK (minor_units BETWEEN 0 AND 4),
 name_en text NOT NULL, name_zh text NOT NULL
);

CREATE TABLE categories (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL,
 parent_id uuid, kind text NOT NULL CHECK (kind IN ('income','expense')),
 name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
 name_zh text NOT NULL DEFAULT '', system_code text,
 archived boolean NOT NULL DEFAULT false, revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
 UNIQUE (tenant_id,book_id,id), UNIQUE (tenant_id,book_id,system_code),
 FOREIGN KEY (tenant_id,book_id) REFERENCES books(tenant_id,id),
 FOREIGN KEY (tenant_id,book_id,parent_id) REFERENCES categories(tenant_id,book_id,id),
 CHECK (parent_id IS DISTINCT FROM id)
);
CREATE TABLE counterparties (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
 archived boolean NOT NULL DEFAULT false, revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
 UNIQUE (tenant_id,id)
);
CREATE TABLE accounts (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL,
 name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
 role text NOT NULL CHECK (role IN ('asset','income','expense','equity')),
 kind text NOT NULL CHECK (kind IN ('cash','bank','wallet','other','system')),
 currency text NOT NULL REFERENCES currencies(code), category_id uuid,
 system_code text, archived boolean NOT NULL DEFAULT false,
 revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
 UNIQUE (tenant_id,book_id,id), UNIQUE (tenant_id,book_id,system_code,currency),
 UNIQUE (tenant_id,book_id,category_id,currency),
 FOREIGN KEY (tenant_id,book_id) REFERENCES books(tenant_id,id),
 FOREIGN KEY (tenant_id,book_id,category_id) REFERENCES categories(tenant_id,book_id,id),
 CHECK ((role='asset' AND kind<>'system' AND category_id IS NULL AND system_code IS NULL)
 OR (role IN ('income','expense') AND kind='system' AND category_id IS NOT NULL)
 OR (role='equity' AND kind='system' AND category_id IS NULL AND system_code='opening'))
);
CREATE TABLE transactions (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL,
 revision integer NOT NULL CHECK (revision > 0), status text NOT NULL CHECK (status IN ('posted','void')),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (tenant_id,book_id,id), FOREIGN KEY (tenant_id,book_id) REFERENCES books(tenant_id,id)
);
CREATE TABLE transaction_revisions (
 tenant_id uuid NOT NULL, book_id uuid NOT NULL, transaction_id uuid NOT NULL,
 revision integer NOT NULL CHECK (revision > 0), kind text NOT NULL CHECK (kind IN ('opening','income','expense','transfer','refund')),
 occurred_on date NOT NULL, account_id uuid NOT NULL, to_account_id uuid, category_id uuid, counterparty_id uuid,
 amount numeric NOT NULL CHECK (amount <> 'NaN'::numeric AND abs(amount)<1000000000000000000),
 to_amount numeric CHECK (to_amount <> 'NaN'::numeric AND to_amount>0 AND to_amount<1000000000000000000),
 original_id uuid, data jsonb NOT NULL, voided boolean NOT NULL DEFAULT false,
 actor_id uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (transaction_id,revision), UNIQUE (tenant_id,book_id,transaction_id,revision),
 FOREIGN KEY (tenant_id,book_id,transaction_id) REFERENCES transactions(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,account_id) REFERENCES accounts(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,to_account_id) REFERENCES accounts(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,category_id) REFERENCES categories(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,counterparty_id) REFERENCES counterparties(tenant_id,id),
 FOREIGN KEY (tenant_id,book_id,original_id) REFERENCES transactions(tenant_id,book_id,id),
 CHECK ((kind='opening' AND amount<>0) OR (kind<>'opening' AND amount>0)),
 CHECK ((kind='transfer') = (to_account_id IS NOT NULL AND to_amount IS NOT NULL)),
 CHECK ((kind IN ('income','expense','refund')) = (category_id IS NOT NULL)),
 CHECK ((kind='refund') = (original_id IS NOT NULL))
);
ALTER TABLE transactions ADD CONSTRAINT transaction_current_revision_fk
 FOREIGN KEY (tenant_id,book_id,id,revision) REFERENCES transaction_revisions(tenant_id,book_id,transaction_id,revision)
 DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE journal_entries (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL,
 transaction_id uuid NOT NULL, revision integer NOT NULL, occurred_on date NOT NULL,
 valuation_currency text NOT NULL REFERENCES currencies(code),
 reversal_of uuid UNIQUE, sealed boolean NOT NULL DEFAULT false,
 UNIQUE (tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,transaction_id,revision) REFERENCES transaction_revisions(tenant_id,book_id,transaction_id,revision),
 FOREIGN KEY (tenant_id,book_id,reversal_of) REFERENCES journal_entries(tenant_id,book_id,id)
);
CREATE UNIQUE INDEX journal_revision_post ON journal_entries(transaction_id,revision) WHERE reversal_of IS NULL;
CREATE TABLE postings (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL, journal_id uuid NOT NULL, account_id uuid NOT NULL,
 amount numeric NOT NULL CHECK (amount <> 'NaN'::numeric AND amount<>0 AND abs(amount)<1000000000000000000),
 valuation_amount numeric NOT NULL CHECK (valuation_amount <> 'NaN'::numeric AND valuation_amount<>0 AND abs(valuation_amount)<1000000000000000000),
 rate_numerator numeric NOT NULL CHECK (rate_numerator <> 'NaN'::numeric AND rate_numerator>0 AND rate_numerator=trunc(rate_numerator) AND rate_numerator<1e40),
 rate_denominator numeric NOT NULL CHECK (rate_denominator <> 'NaN'::numeric AND rate_denominator>0 AND rate_denominator=trunc(rate_denominator) AND rate_denominator<1e40),
 rate_source text NOT NULL CHECK (rate_source IN ('identity','manual_actual')),
 rate_date date NOT NULL,
 FOREIGN KEY (tenant_id,book_id,journal_id) REFERENCES journal_entries(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,account_id) REFERENCES accounts(tenant_id,book_id,id),
 CHECK (amount*rate_numerator=valuation_amount*rate_denominator)
);
CREATE INDEX postings_account ON postings(tenant_id,book_id,account_id);
CREATE INDEX postings_journal ON postings(journal_id);
CREATE TABLE transaction_links (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, book_id uuid NOT NULL,
 source_id uuid NOT NULL, target_id uuid NOT NULL, kind text NOT NULL CHECK (kind IN ('related','fee','refund')),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (tenant_id,book_id,source_id) REFERENCES transactions(tenant_id,book_id,id),
 FOREIGN KEY (tenant_id,book_id,target_id) REFERENCES transactions(tenant_id,book_id,id),
 CHECK (source_id<>target_id), CHECK (kind<>'related' OR source_id<target_id),
 UNIQUE (source_id,target_id,kind)
);
CREATE UNIQUE INDEX transaction_one_fee_parent ON transaction_links(target_id) WHERE kind='fee';
CREATE UNIQUE INDEX transaction_one_refund_parent ON transaction_links(target_id) WHERE kind='refund';
CREATE TABLE accounting_operations (
 tenant_id uuid NOT NULL REFERENCES tenants(id), key uuid NOT NULL, request_hash text NOT NULL,
 response jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,key)
);
CREATE TABLE accounting_audit (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id), book_id uuid,
 actor_id uuid NOT NULL REFERENCES users(id), action text NOT NULL, resource_id uuid NOT NULL,
 before_data jsonb, after_data jsonb, created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (tenant_id,book_id) REFERENCES books(tenant_id,id)
);
CREATE INDEX transactions_book ON transactions(tenant_id,book_id,created_at DESC,id DESC);
CREATE INDEX revisions_date ON transaction_revisions(tenant_id,book_id,occurred_on DESC);

-- +goose StatementBegin
CREATE FUNCTION accounting_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'sealed accounting history is immutable' USING ERRCODE='23514'; END $$;
-- +goose StatementEnd
CREATE TRIGGER revisions_immutable BEFORE UPDATE OR DELETE ON transaction_revisions FOR EACH ROW EXECUTE FUNCTION accounting_immutable();
CREATE TRIGGER postings_immutable BEFORE UPDATE OR DELETE ON postings FOR EACH ROW EXECUTE FUNCTION accounting_immutable();
CREATE TRIGGER audit_immutable BEFORE UPDATE OR DELETE ON accounting_audit FOR EACH ROW EXECUTE FUNCTION accounting_immutable();
CREATE TRIGGER operations_immutable BEFORE UPDATE OR DELETE ON accounting_operations FOR EACH ROW EXECUTE FUNCTION accounting_immutable();

-- +goose StatementBegin
CREATE FUNCTION accounting_reference_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent categories;
BEGIN
 IF TG_TABLE_NAME='accounts' THEN
  IF TG_OP='UPDATE' AND (NEW.currency,NEW.role,NEW.kind,NEW.category_id,NEW.tenant_id,NEW.book_id,NEW.system_code)
   IS DISTINCT FROM (OLD.currency,OLD.role,OLD.kind,OLD.category_id,OLD.tenant_id,OLD.book_id,OLD.system_code) THEN
   RAISE EXCEPTION 'account identity is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  IF TG_OP='UPDATE' AND (NEW.parent_id,NEW.kind,NEW.tenant_id,NEW.book_id,NEW.system_code)
   IS DISTINCT FROM (OLD.parent_id,OLD.kind,OLD.tenant_id,OLD.book_id,OLD.system_code) THEN
   RAISE EXCEPTION 'category identity is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.parent_id IS NOT NULL THEN
   SELECT * INTO parent FROM categories WHERE id=NEW.parent_id;
   IF parent.parent_id IS NOT NULL OR parent.kind<>NEW.kind OR (parent.archived AND NOT NEW.archived) THEN
    RAISE EXCEPTION 'invalid category parent' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER account_guard BEFORE INSERT OR UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION accounting_reference_guard();
CREATE TRIGGER category_guard BEFORE INSERT OR UPDATE ON categories FOR EACH ROW EXECUTE FUNCTION accounting_reference_guard();

-- +goose StatementBegin
CREATE FUNCTION accounting_posting_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j journal_entries; a accounts; digits integer; value_digits integer;
BEGIN
 SELECT * INTO j FROM journal_entries WHERE id=NEW.journal_id FOR UPDATE;
 IF j.sealed THEN RAISE EXCEPTION 'cannot append to a sealed journal' USING ERRCODE='23514'; END IF;
 SELECT * INTO a FROM accounts WHERE id=NEW.account_id;
 SELECT minor_units INTO digits FROM currencies WHERE code=a.currency;
 SELECT minor_units INTO value_digits FROM currencies WHERE code=j.valuation_currency;
 IF NEW.amount<>trunc(NEW.amount,digits) OR NEW.valuation_amount<>trunc(NEW.valuation_amount,value_digits)
 OR (a.currency=j.valuation_currency AND (NEW.amount<>NEW.valuation_amount OR NEW.rate_numerator<>NEW.rate_denominator OR NEW.rate_source<>'identity'))
 OR (a.currency<>j.valuation_currency AND NEW.rate_source<>'manual_actual')
 OR NEW.rate_date<>j.occurred_on THEN
  RAISE EXCEPTION 'invalid posting precision or rate' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER posting_guard BEFORE INSERT ON postings FOR EACH ROW EXECUTE FUNCTION accounting_posting_guard();

-- +goose StatementBegin
CREATE FUNCTION accounting_journal_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.sealed OR NOT NEW.sealed OR
  (to_jsonb(NEW)-'sealed') IS DISTINCT FROM (to_jsonb(OLD)-'sealed') THEN
  RAISE EXCEPTION 'journal permits only sealing' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER journal_guard BEFORE UPDATE OR DELETE ON journal_entries FOR EACH ROW EXECUTE FUNCTION accounting_journal_guard();

-- +goose StatementBegin
CREATE FUNCTION accounting_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE j journal_entries; original journal_entries; n bigint; total numeric;
BEGIN
 SELECT * INTO j FROM journal_entries WHERE id=NEW.id;
 SELECT count(*),sum(valuation_amount) INTO n,total FROM postings WHERE journal_id=j.id;
 IF NOT j.sealed OR n<2 OR total<>0 THEN
  RAISE EXCEPTION 'journal must be sealed and balanced' USING ERRCODE='23514';
 END IF;
 IF j.reversal_of IS NOT NULL THEN
  SELECT * INTO original FROM journal_entries WHERE id=j.reversal_of;
  IF original.reversal_of IS NOT NULL OR original.transaction_id<>j.transaction_id
   OR original.revision<>j.revision-1 OR original.occurred_on<>j.occurred_on
   OR original.valuation_currency<>j.valuation_currency THEN
   RAISE EXCEPTION 'reversal must retain transaction, date and currency' USING ERRCODE='23514';
  END IF;
 END IF;
 IF j.reversal_of IS NOT NULL AND EXISTS (
  (SELECT account_id,amount,valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date FROM postings WHERE journal_id=j.reversal_of
   EXCEPT ALL SELECT account_id,-amount,-valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date FROM postings WHERE journal_id=j.id)
  UNION ALL
  (SELECT account_id,-amount,-valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date FROM postings WHERE journal_id=j.id
   EXCEPT ALL SELECT account_id,amount,valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date FROM postings WHERE journal_id=j.reversal_of)
 ) THEN RAISE EXCEPTION 'reversal must exactly negate the original' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER balanced_journal AFTER INSERT OR UPDATE ON journal_entries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION accounting_balanced();

-- +goose StatementBegin
CREATE FUNCTION accounting_complete_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE posted bigint; reversed bigint; a accounts; digits integer;
BEGIN
 SELECT count(*) FILTER (WHERE reversal_of IS NULL),count(*) FILTER (WHERE reversal_of IS NOT NULL)
 INTO posted,reversed FROM journal_entries WHERE transaction_id=NEW.transaction_id AND revision=NEW.revision;
 IF posted<>(CASE WHEN NEW.voided THEN 0 ELSE 1 END)
 OR reversed<>(CASE WHEN NEW.revision=1 THEN 0 ELSE 1 END)
 OR (NEW.revision=1 AND NEW.voided) THEN
  RAISE EXCEPTION 'revision requires its posting and predecessor reversal' USING ERRCODE='23514';
 END IF;
 SELECT * INTO a FROM accounts WHERE id=NEW.account_id;
 SELECT minor_units INTO digits FROM currencies WHERE code=a.currency;
 IF a.role<>'asset' OR NEW.amount<>trunc(NEW.amount,digits)
 OR EXISTS (SELECT 1 FROM journal_entries WHERE transaction_id=NEW.transaction_id AND revision=NEW.revision
 AND reversal_of IS NULL AND (occurred_on<>NEW.occurred_on OR valuation_currency<>a.currency)) THEN
  RAISE EXCEPTION 'revision has invalid account precision date or currency' USING ERRCODE='23514';
 END IF;
 IF NEW.to_account_id IS NOT NULL THEN
  SELECT * INTO a FROM accounts WHERE id=NEW.to_account_id;
  SELECT minor_units INTO digits FROM currencies WHERE code=a.currency;
  IF a.role<>'asset' OR NEW.account_id=NEW.to_account_id OR NEW.to_amount<>trunc(NEW.to_amount,digits) THEN
   RAISE EXCEPTION 'invalid destination account or precision' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER complete_revision AFTER INSERT ON transaction_revisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION accounting_complete_revision();

-- +goose StatementBegin
CREATE FUNCTION accounting_transaction_json(t transactions) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('id',t.id,'revision',t.revision,'status',t.status,'created_at',t.created_at,'data',r.data)
 FROM transaction_revisions r WHERE r.transaction_id=t.id AND r.revision=t.revision
$$;
-- +goose StatementEnd

INSERT INTO currencies(code,minor_units,name_en,name_zh) VALUES
('AED',2,'United Arab Emirates Dirham','阿联酋迪拉姆'),
('AFN',0,'Afghan Afghani','阿富汗尼'),
('ALL',0,'Albanian Lek','阿尔巴尼亚列克'),
('AMD',2,'Armenian Dram','亚美尼亚德拉姆'),
('AOA',2,'Angolan Kwanza','安哥拉宽扎'),
('ARS',2,'Argentine Peso','阿根廷比索'),
('AUD',2,'Australian Dollar','澳大利亚元'),
('AWG',2,'Aruban Florin','阿鲁巴弗罗林'),
('AZN',2,'Azerbaijani Manat','阿塞拜疆马纳特'),
('BAM',2,'Bosnia-Herzegovina Convertible Mark','波斯尼亚-黑塞哥维那可兑换马克'),
('BBD',2,'Barbadian Dollar','巴巴多斯元'),
('BDT',2,'Bangladeshi Taka','孟加拉塔卡'),
('BHD',3,'Bahraini Dinar','巴林第纳尔'),
('BIF',0,'Burundian Franc','布隆迪法郎'),
('BMD',2,'Bermudan Dollar','百慕大元'),
('BND',2,'Brunei Dollar','文莱元'),
('BOB',2,'Bolivian Boliviano','玻利维亚诺'),
('BRL',2,'Brazilian Real','巴西雷亚尔'),
('BSD',2,'Bahamian Dollar','巴哈马元'),
('BTN',2,'Bhutanese Ngultrum','不丹努尔特鲁姆'),
('BWP',2,'Botswanan Pula','博茨瓦纳普拉'),
('BYN',2,'Belarusian Ruble','白俄罗斯卢布'),
('BZD',2,'Belize Dollar','伯利兹元'),
('CAD',2,'Canadian Dollar','加拿大元'),
('CDF',2,'Congolese Franc','刚果法郎'),
('CHF',2,'Swiss Franc','瑞士法郎'),
('CLP',0,'Chilean Peso','智利比索'),
('CNY',2,'Chinese Yuan','人民币'),
('COP',0,'Colombian Peso','哥伦比亚比索'),
('CRC',2,'Costa Rican Colón','哥斯达黎加科朗'),
('CUP',2,'Cuban Peso','古巴比索'),
('CVE',2,'Cape Verdean Escudo','佛得角埃斯库多'),
('CZK',2,'Czech Koruna','捷克克朗'),
('DJF',0,'Djiboutian Franc','吉布提法郎'),
('DKK',2,'Danish Krone','丹麦克朗'),
('DOP',2,'Dominican Peso','多米尼加比索'),
('DZD',2,'Algerian Dinar','阿尔及利亚第纳尔'),
('EGP',2,'Egyptian Pound','埃及镑'),
('ERN',2,'Eritrean Nakfa','厄立特里亚纳克法'),
('ETB',2,'Ethiopian Birr','埃塞俄比亚比尔'),
('EUR',2,'Euro','欧元'),
('FJD',2,'Fijian Dollar','斐济元'),
('FKP',2,'Falkland Islands Pound','福克兰群岛镑'),
('GBP',2,'British Pound','英镑'),
('GEL',2,'Georgian Lari','格鲁吉亚拉里'),
('GHS',2,'Ghanaian Cedi','加纳塞地'),
('GIP',2,'Gibraltar Pound','直布罗陀镑'),
('GMD',2,'Gambian Dalasi','冈比亚达拉西'),
('GNF',0,'Guinean Franc','几内亚法郎'),
('GTQ',2,'Guatemalan Quetzal','危地马拉格查尔'),
('GYD',2,'Guyanaese Dollar','圭亚那元'),
('HKD',2,'Hong Kong Dollar','港元'),
('HNL',2,'Honduran Lempira','洪都拉斯伦皮拉'),
('HTG',2,'Haitian Gourde','海地古德'),
('HUF',0,'Hungarian Forint','匈牙利福林'),
('IDR',0,'Indonesian Rupiah','印度尼西亚卢比'),
('ILS',2,'Israeli New Shekel','以色列新谢克尔'),
('INR',2,'Indian Rupee','印度卢比'),
('IQD',0,'Iraqi Dinar','伊拉克第纳尔'),
('IRR',0,'Iranian Rial','伊朗里亚尔'),
('ISK',0,'Icelandic Króna','冰岛克朗'),
('JMD',2,'Jamaican Dollar','牙买加元'),
('JOD',3,'Jordanian Dinar','约旦第纳尔'),
('JPY',0,'Japanese Yen','日元'),
('KES',2,'Kenyan Shilling','肯尼亚先令'),
('KGS',2,'Kyrgyz Som','吉尔吉斯斯坦索姆'),
('KHR',2,'Cambodian Riel','柬埔寨瑞尔'),
('KMF',0,'Comorian Franc','科摩罗法郎'),
('KPW',0,'North Korean Won','朝鲜元'),
('KRW',0,'South Korean Won','韩元'),
('KWD',3,'Kuwaiti Dinar','科威特第纳尔'),
('KYD',2,'Cayman Islands Dollar','开曼元'),
('KZT',2,'Kazakhstani Tenge','哈萨克斯坦坚戈'),
('LAK',0,'Laotian Kip','老挝基普'),
('LBP',0,'Lebanese Pound','黎巴嫩镑'),
('LKR',2,'Sri Lankan Rupee','斯里兰卡卢比'),
('LRD',2,'Liberian Dollar','利比里亚元'),
('LSL',2,'Lesotho Loti','莱索托洛蒂'),
('LYD',3,'Libyan Dinar','利比亚第纳尔'),
('MAD',2,'Moroccan Dirham','摩洛哥迪拉姆'),
('MDL',2,'Moldovan Leu','摩尔多瓦列伊'),
('MGA',0,'Malagasy Ariary','马达加斯加阿里亚里'),
('MKD',2,'Macedonian Denar','马其顿第纳尔'),
('MMK',0,'Myanmar Kyat','缅甸元'),
('MNT',2,'Mongolian Tugrik','蒙古图格里克'),
('MOP',2,'Macanese Pataca','澳门币'),
('MUR',2,'Mauritian Rupee','毛里求斯卢比'),
('MVR',2,'Maldivian Rufiyaa','马尔代夫卢菲亚'),
('MWK',2,'Malawian Kwacha','马拉维克瓦查'),
('MXN',2,'Mexican Peso','墨西哥比索'),
('MYR',2,'Malaysian Ringgit','马来西亚林吉特'),
('MZN',2,'Mozambican Metical','莫桑比克美提卡'),
('NAD',2,'Namibian Dollar','纳米比亚元'),
('NGN',2,'Nigerian Naira','尼日利亚奈拉'),
('NIO',2,'Nicaraguan Córdoba','尼加拉瓜科多巴'),
('NOK',2,'Norwegian Krone','挪威克朗'),
('NPR',2,'Nepalese Rupee','尼泊尔卢比'),
('NZD',2,'New Zealand Dollar','新西兰元'),
('OMR',3,'Omani Rial','阿曼里亚尔'),
('PAB',2,'Panamanian Balboa','巴拿马巴波亚'),
('PEN',2,'Peruvian Sol','秘鲁索尔'),
('PGK',2,'Papua New Guinean Kina','巴布亚新几内亚基那'),
('PHP',2,'Philippine Peso','菲律宾比索'),
('PKR',0,'Pakistani Rupee','巴基斯坦卢比'),
('PLN',2,'Polish Zloty','波兰兹罗提'),
('PYG',0,'Paraguayan Guarani','巴拉圭瓜拉尼'),
('QAR',2,'Qatari Riyal','卡塔尔里亚尔'),
('RON',2,'Romanian Leu','罗马尼亚列伊'),
('RSD',0,'Serbian Dinar','塞尔维亚第纳尔'),
('RUB',2,'Russian Ruble','俄罗斯卢布'),
('RWF',0,'Rwandan Franc','卢旺达法郎'),
('SAR',2,'Saudi Riyal','沙特里亚尔'),
('SBD',2,'Solomon Islands Dollar','所罗门群岛元'),
('SCR',2,'Seychellois Rupee','塞舌尔卢比'),
('SDG',2,'Sudanese Pound','苏丹镑'),
('SEK',2,'Swedish Krona','瑞典克朗'),
('SGD',2,'Singapore Dollar','新加坡元'),
('SHP',2,'St. Helena Pound','圣赫勒拿群岛磅'),
('SOS',0,'Somali Shilling','索马里先令'),
('SRD',2,'Surinamese Dollar','苏里南元'),
('SSP',2,'South Sudanese Pound','南苏丹镑'),
('STN',2,'São Tomé & Príncipe Dobra','圣多美和普林西比多布拉'),
('SYP',0,'Syrian Pound','叙利亚镑'),
('SZL',2,'Swazi Lilangeni','斯威士兰里兰吉尼'),
('THB',2,'Thai Baht','泰铢'),
('TJS',2,'Tajikistani Somoni','塔吉克斯坦索莫尼'),
('TMT',2,'Turkmenistani Manat','土库曼斯坦马纳特'),
('TND',3,'Tunisian Dinar','突尼斯第纳尔'),
('TOP',2,'Tongan Paʻanga','汤加潘加'),
('TRY',2,'Turkish Lira','土耳其里拉'),
('TTD',2,'Trinidad & Tobago Dollar','特立尼达和多巴哥元'),
('TWD',2,'New Taiwan Dollar','新台币'),
('TZS',2,'Tanzanian Shilling','坦桑尼亚先令'),
('UAH',2,'Ukrainian Hryvnia','乌克兰格里夫纳'),
('UGX',0,'Ugandan Shilling','乌干达先令'),
('USD',2,'US Dollar','美元'),
('UYU',2,'Uruguayan Peso','乌拉圭比索'),
('UZS',2,'Uzbekistani Som','乌兹别克斯坦苏姆'),
('VND',0,'Vietnamese Dong','越南盾'),
('VUV',0,'Vanuatu Vatu','瓦努阿图瓦图'),
('WST',2,'Samoan Tala','萨摩亚塔拉'),
('XAF',0,'Central African CFA Franc','中非法郎'),
('XCD',2,'East Caribbean Dollar','东加勒比元'),
('XOF',0,'West African CFA Franc','西非法郎'),
('XPF',0,'CFP Franc','太平洋法郎'),
('YER',0,'Yemeni Rial','也门里亚尔'),
('ZAR',2,'South African Rand','南非兰特'),
('ZMW',2,'Zambian Kwacha','赞比亚克瓦查');

-- +goose StatementBegin
CREATE FUNCTION accounting_seed_categories() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE root_id uuid;
BEGIN
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'expense','Food and drink','餐饮','food');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Meals','餐食','meals');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Drinks','饮品','drinks');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'expense','Transport','交通','transport');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Public transport','公共交通','public_transport');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Taxi','打车','taxi');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'expense','Home','居家','home');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Housing','住房','housing');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Daily essentials','日用品','essentials');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'expense','Financial fees','金融费用','finance');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'expense','Fees','手续费','fees');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'expense','Other expenses','其他支出','other_expense');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'income','Employment','职业收入','employment');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'income','Salary','工资','salary');
 INSERT INTO categories(id,tenant_id,book_id,parent_id,kind,name,name_zh,system_code) VALUES(gen_random_uuid(),NEW.tenant_id,NEW.id,root_id,'income','Bonus','奖金','bonus');
 root_id:=gen_random_uuid();
 INSERT INTO categories(id,tenant_id,book_id,kind,name,name_zh,system_code) VALUES(root_id,NEW.tenant_id,NEW.id,'income','Other income','其他收入','other_income');
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER accounting_book_categories AFTER INSERT ON books FOR EACH ROW EXECUTE FUNCTION accounting_seed_categories();
CREATE TEMP TABLE accounting_existing_books AS SELECT * FROM books WITH NO DATA;
CREATE TRIGGER accounting_existing_categories AFTER INSERT ON accounting_existing_books FOR EACH ROW EXECUTE FUNCTION accounting_seed_categories();
INSERT INTO accounting_existing_books SELECT * FROM books;
DROP TABLE accounting_existing_books;

-- +goose Down
DROP TRIGGER accounting_book_categories ON books;
DROP FUNCTION accounting_seed_categories();
DROP FUNCTION accounting_transaction_json(transactions);
DROP TRIGGER complete_revision ON transaction_revisions;
DROP FUNCTION accounting_complete_revision();
DROP TRIGGER balanced_journal ON journal_entries;
DROP FUNCTION accounting_balanced();
DROP TRIGGER journal_guard ON journal_entries;
DROP FUNCTION accounting_journal_guard();
DROP TRIGGER posting_guard ON postings;
DROP FUNCTION accounting_posting_guard();
DROP TRIGGER account_guard ON accounts;
DROP TRIGGER category_guard ON categories;
DROP FUNCTION accounting_reference_guard();
DROP TABLE accounting_audit,accounting_operations,transaction_links,postings,journal_entries;
ALTER TABLE transactions DROP CONSTRAINT transaction_current_revision_fk;
DROP TABLE transaction_revisions,transactions,accounts,counterparties,categories,currencies;
DROP FUNCTION accounting_immutable();
