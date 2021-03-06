# GTM

[English](https://github.com/quanhengzhuang/gtm/blob/master/README.md)

GTM 的全称是 `Global Transaction Manager`，是一个解决分布式事务问题的框架，基于 Go 编写。

使用 GTM 相比 2PC 类框架更简单。GTM 的原理基于 2PC 协议，并通过判断参与者的执行顺序，允许部分参与者省略回滚阶段，从而降低业务的接入成本，提升易用性。基于长期在交易类系统上的实践经验，对大量业务场景总结思考，我们发现，在参与者执行顺序确定的前提下，很多时候业务并不需要实现回滚接口。另外，GTM 是 2PC 的超集，可以实现 2PC 的所有场景。

一个 GTM 事务由多个 `Partner` 组成，Partner 是实际业务的封装单元。Partner 在 GTM 中被划分为以下三类：
1. `NormalPartner`：支持回滚的业务（例如余额扣减 + 余额冲正）
2. `UncertainPartner`：不支持回滚且可能失败的业务（例如一次抢购行为，且抢购未实现回滚）
3. `CertainPartner`：不支持回滚但能确保成功的业务（例如发红包）

将业务套用以上三种 Partner，GTM 能保证这些 Partner 执行结果的一致性，即要么全部成功，要么全部失败。

## 举例说明
拿一个最简单的场景举例：`A 给 B 转账10元钱`。

2PC 模式需要实现以下`6个接口`：

| | Prepare | Commmit | Rollback |
| - | :-: | :-: | :-: |
| A | 1. 冻结余额10元 | 3. 扣除冻结10元 | 5. 解除冻结10元 |
| B | 2. 增加冻结10元 | 4. 解除冻结10元 | 6. 扣减冻结10元 |

GTM 模式只需要实现`2个接口`：

| | Do | DoNext | Undo |
| - | :-: | :-: | :-: |
| A (UncertainPartner) | 1. 余额扣减10元 | | |
| B (CertainPartner) | | 2. 余额增加10元 | |

### 为什么 B 不需要实现回滚？
我们认为 B 的加钱操作，在`实际业务场景中`的结果是确定性的，即可以确保成功。即使暂时不成功，也能在重试后成功。

### 为什么 A 不需要实现回滚？
虽然 A 有可能因余额不足等原因执行失败，但 A 返回失败后，GTM 就不会继续往下调用了，此时等同于什么都没发生。

### 为什么我们希望回滚实现的越少越好？
实际的业务场景要远比上面的例子复杂，我们考虑到以下几点：

- 实现回滚逻辑要增加`额外的工作量`；
- 有些业务`无法或很难提供回滚`，如第三方接口；
- 回滚相比正常逻辑更`容易产生 BUG`，一是容易疏于维护，二是容易疏于测试；

GTM 需要你根据业务场景，套用三种不同的参与者（Partner），仅有第一种参与者需要实现回滚。这会增加一点学习成本，但会显著降低开发和维护的成本。

## 实现一个 Partner
一个 GTM 事务由多个 `Partner` 构成，每个 Partner 对应一个实际的业务逻辑。每个 Partner 根据业务场景需要实现 `Do()/DoNext()/Undo()` 方法集中的一个或多个，其中每个方法应该是一个原子操作，并需要保证幂等。

Partner 分为以下三种：
- `NormalPartner` 是需要支持回滚的参与者。你可以实现 `Do() + Undo()` 用于执行业务逻辑和回滚业务逻辑，类似 Saga 模式，也是推荐的模式，此时 DoNext() 可以直接返回 true，不用实现。你也可以实现完整的 `Do() + DoNext() + Undo()` 用于锁定资源、执行业务逻辑、解锁资源，类似 2PC 模式。NormalPartner 可以有任意个，在事务中最先执行。
- `UncertainPartner` 是不需要支持回滚的参与者，并且结果可能成功可能失败。只需要实现一个 `Do()` 方法。 UncertainPartner 最多只能有一个，在 NormalPartner 之后执行。
- `CertainPartner` 是不需要支持回滚的参与者，但能确保执行成功。只需要实现一个 `DoNext()` 方法。 CertainPartner 可以任意个， 在 UncertainPartner 之后执行。

### Partner 需要实现的方法
Partner 的接口定义可参见 [partner.go](https://github.com/quanhengzhuang/gtm/blob/master/partner.go)。

| | Do() | DoNext() | Undo() |
| - | :-: | :-: | :-: |
| NormalPartner | 是 | 可选 | 是 |
| UncertainPartner | 是 | | |
| CertainPartner | | 是 | |

方法需要遵守以下约定：
- Undo() 需要回滚 Do() 的操作；
- 所有的方法必须实现幂等；

### Do()/DoNext()/Undo() 的返回值约定
方法的返回值必须要遵循以下约定，如果未得到期望的返回值，GTM 将会重试执行该方法。

| | 期望的返回值 |
| - | - |
| Do() of NormalPartner | Success / Fail / Uncertain / Error |
| Do() of UncertainPartner | Success / Fail |
| DoNext() | Success |
| Undo() | Success |

特别说明：
- Success 表示执行成功，Fail 表示执行失败，Uncertain 表示结果不确定；
- Do() 返回为 Fail 会认为未产生作用，不会调用该 Partner 的 Undo()；
- 当结果为 Fail / Uncertain 时可以同时返回 Error；
- DoNext()/Undo() 返回 Error 会认为未执行成功，会重试；

### Partner 为什么要分三种类型？

为了减少回滚逻辑的实现。如上面曾提到的，回滚接口不但增加开发成本，也增加程度的复杂度，同时因为容易被测试疏忽，相比正常逻辑更容易产生 BUG。

给业务场景分类会增加一点心智成本，但当我们实现业务需求时，理所应当要深入理解业务。

## 开始使用

### 安装
```
go get github.com/quanhengzhuang/gtm
```

### 设置存储引擎
GTM 的 Partner 和执行状态需要存储，GTM 允许开发者选用不同的存储引擎，或实现自己的存储引擎，以达到性能最优。

`DBStorage` 是 GTM 内置的一个存储引擎，使用数据库来存储事务的数据与状态。（可以使用任意类型的数据库，建议是一个单独的实例）

如果使用 DBStorage，需要增加以下初始化操作：
```go
db, err := gorm.Open("mysql", "root:root1234@/gtm?charset=utf8&parseTime=True&loc=Local")
if err != nil {
	log.Fatalf("db open failed: %v", err)
}

gtm.SetStorage(gtm.NewDBStorage(db))
```

如果使用 DBStorage，需要创建以下表：
```sql
DROP TABLE gtm_transactions;
CREATE TABLE gtm_transactions (
	id         bigint UNSIGNED NOT NULL AUTO_INCREMENT,
	name       varchar(50) NOT NULL,
	times      int UNSIGNED NOT NULL,
	retry_at   timestamp NOT NULL,
	timeout    int UNSIGNED NOT NULL,
	result     varchar(20) NOT NULL,
	content    mediumtext,
	created_at timestamp NOT NULL,
	updated_at timestamp NOT NULL,

	PRIMARY KEY (id),
	KEY idx_retry (result, retry_at)
);

DROP TABLE gtm_partner_result;
CREATE TABLE gtm_partner_result (
	id              bigint UNSIGNED NOT NULL AUTO_INCREMENT,
	transaction_id  bigint UNSIGNED NOT NULL,
	phase           varchar(20) NOT NULL,
	offset          tinyint UNSIGNED NOT NULL,
	result          varchar(20) NOT NULL,
	cost            int UNSIGNED NOT NULL,
	created_at      timestamp NOT NULL,
	updated_at      timestamp NOT NULL,

	PRIMARY KEY (id),
	UNIQUE KEY uni_tx_id (transaction_id, phase, offset)
);
```

### 开始一个新事务
每个事务的返回结果可能有三种，即 Success（成功）、Fail（失败）、Uncertain（不确定）。

```go
tx := gtm.New()
tx.AddNormal(&Payer{OrderID: "100001", UserID: 20001, Amount: 99})
tx.AddUncertain(&OrderCreator{OrderID: "100001", UserID: 20001, ProductID: 31, Amount: 99})

switch result, err := tx.Execute(); result {
case gtm.Success:
	t.Logf("tx's result = success")
case gtm.Fail:
	t.Logf("tx's result = fail. err = %+v", err)
case gtm.Uncertain:
	t.Logf("tx's result = uncertain: err = %v", err)
}
```

### 异步执行
可以使用异步执行来提升事务的执行效率，GTM 支持部分异步执行或全部异步执行。

CertainPartner 在事务中可以设置为异步执行，此时 tx.Execute() 依然可以返回执行结果：
```go
tx.AddAsync(/* a CertainPartner */)
```

整个事务也可以设置为异步执行，此时不能得到事务执行结果：
```go
tx.ExecuteAsync() // 替代 tx.Execute()
```

### 重试超时的事务（事务补偿）

`RetryTimeoutTransactions` 用于重试执行超时的事务，可以设置每次重试的数量，返回值为重试的事务详情、重试的结果、重试中遇到的错误。

```go
transactions, results, errs, err := gtm.RetryTimeoutTransactions(10)
```

以上逻辑可以放到一个定时任务中执行。

## 定制存储引擎

除了使用内置的 `DBStorage`，你可以定制实现自己的存储引擎，以达到最优的性能。你需要实现 `gtm.Storage` 接口，参见 [storage.go](https://github.com/quanhengzhuang/gtm/blob/master/storage.go)。

推荐使用`持久化存储`存放事务数据与事务状态，使用高速的`内存存储`存放参与者的执行状态，如 MySQL + Redis 混合使用。事务数据和状态只会在执行前和执行后各产生一次写操作，丢失可能会产生不一致，后果严重；参与者状态在每个方法执行后写一次，写次数和参与者数量相关，如果状态丢失会导致重试，而参与者都需要实现幂等，所以不会有一致性问题。

## 关于隔离性
和大多数分布式事务的解决方案一样，GTM 默认不会产生隔离性，依赖业务的具体实现，可能会有脏读问题。但对于多数业务场景来说，脏读是可接受的，因为是小概率事件，而且只会影响体验。

如果要解决脏读问题，可以实现一个 `LockPartner`，如下所示：
```go
type LockPartner struct{}

func (l *Lock) Do() { /* lock */ }

func (l *Lock) DoNext() { /* unlock */ }

func (l *Lock) Undo() { /* unlock */ }
```

将 LockPartner 作为 GTM 的第一个参与者，并在读的地方判断锁：
```go
if locker.RLock() {
	// You can show directly
} else {
	// You can show "Processing"
}
```

## 更多
https://pkg.go.dev/mod/github.com/quanhengzhuang/gtm

[![Go Report Card](https://goreportcard.com/badge/github.com/quanhengzhuang/gtm)](https://goreportcard.com/report/github.com/quanhengzhuang/gtm)
