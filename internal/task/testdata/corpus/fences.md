# platform migration

- [ ] Document the rollback 🔼
- [ ] Test the drain script

The script emits a checklist that must not be reordered:

```markdown
- [ ] zzz last alphabetically but first in the sample
- [x] aaa
- [ ] mmm 🔺
```

~~~sh
# - [ ] this is a comment inside a tilde fence
echo "- [ ] not a task"
~~~

- [ ] Ship it 🔺

````
```markdown
- [ ] nested fence, still not a task
```
````

- [ ] Last one
