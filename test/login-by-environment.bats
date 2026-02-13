load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

export "${XMC_ENV_PREFIX}_USER=artemis"
export "${XMC_ENV_PREFIX}_PASSWORD=artemis"
export xmc="$XMC_BINARY"

@test "initialize ANYCAST address" {
    run $xmc get queue1
    assert_success
}

@test "send/receive text payload" {
    run $xmc put queue1 HelloWorld
    assert_success

    run $xmc get queue1
    assert_success

    assert_output "HelloWorld"
}

@test "send/receive binary file" {
    run dd if=/dev/urandom of=./test.bin bs=1024 count=1
    assert_success

    run bash -c '$xmc put queue1 < ./test.bin'
    assert_success

    run bash -c '$xmc get queue1 > ./test.out.bin'
    assert_success

    run diff ./test.bin ./test.out.bin
    assert_success
}
