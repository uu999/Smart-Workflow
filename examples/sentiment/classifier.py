# 情感分类器（规则版，无需外部 LLM，样例可离线跑通）。
# 作为 kind=python 的 application，其 config.code 即本段；batch 逐条注入 inputs["item"]。
# item 是 dataset 的一行：{"query": "...", "label": "..."}。
# 输出 query/gold/pred，供下游 code 节点统计准召。

item = inputs.get("item", {})
query = item.get("query", "")
gold = item.get("label", "")

NEG = ["差", "失望", "不来", "太贵", "一般", "少", "慢", "难吃"]
POS = ["好", "棒", "推荐", "热情", "快", "实惠", "好评", "不错"]

neg_hit = sum(1 for w in NEG if w in query)
pos_hit = sum(1 for w in POS if w in query)
pred = "负面" if neg_hit > pos_hit else "正面"

sink({"query": query, "gold": gold, "pred": pred})
